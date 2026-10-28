// reef-agent provides inventory and authorized host operations over outbound HTTPS.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"reef/internal/agents"
	"reef/internal/agentupdate"
	"reef/internal/app"
	"reef/internal/buildinfo"
	"reef/internal/docker"
)

type identity struct {
	URL        string `json:"url"`
	ID         string `json:"id"`
	Credential string `json:"credential"`
}
type client struct {
	url      string
	http     *http.Client
	identity identity
}

func (c *client) post(ctx context.Context, path string, input, output any) error {
	b, e := json.Marshal(input)
	if e != nil {
		return e
	}
	if len(b) > agents.MaxPayload {
		return errors.New("inventory exceeds protocol limit")
	}
	r, e := http.NewRequestWithContext(ctx, "POST", c.url+"/agent/v1/"+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/json")
	if c.identity.ID != "" {
		r.Header.Set("X-Reef-Host", c.identity.ID)
		r.Header.Set("Authorization", "Bearer "+c.identity.Credential)
	}
	res, e := c.http.Do(r)
	if e != nil {
		return errors.New("central Reef is unreachable or TLS verification failed")
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("central Reef rejected %s (HTTP %d)", path, res.StatusCode)
	}
	if output != nil {
		limit := int64(4096)
		if path == "commands" {
			limit = agents.MaxPayload
		}
		return json.NewDecoder(io.LimitReader(res.Body, limit)).Decode(output)
	}
	return nil
}
func privateFile(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
		return nil, errors.New("credential file must be a private regular file, at most 8 KiB")
	}
	return os.ReadFile(path)
}
func save(path string, id identity) error {
	b, e := json.Marshal(id)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".identity-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func run() error {
	central := flag.String("server", "", "Central Reef HTTPS origin")
	state := flag.String("state-dir", "/var/lib/reef-agent/state", "Private persistent agent state directory")
	token := flag.String("enrollment-file", "", "Private one-use enrollment token file (first run only)")
	socket := flag.String("socket", "/var/run/docker.sock", "Local Docker socket")
	ca := flag.String("ca-file", "", "Optional trusted private CA bundle")
	config := flag.String("config", "", "Optional agent project configuration file; roots remain explicitly local")
	worker := flag.Bool("update-worker", false, "Run the host-local agent update service")
	updateDir := flag.String("update-dir", os.Getenv("REEF_AGENT_UPDATE_DIR"), "Optional shared agent update state directory")
	installation := flag.String("update-installation", os.Getenv("REEF_INSTALLATION_DIR"), "Dedicated agent installation for the update service")
	flag.Parse()
	if *worker {
		store, err := agentupdate.Open(*updateDir)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return (&agentupdate.Worker{Store: store, Installation: *installation, Run: agentupdate.DockerRunner(*socket)}).Serve(ctx)
	}
	u, e := url.Parse(*central)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("server must be an HTTPS origin")
	}
	if !filepath.IsAbs(*state) {
		return errors.New("state-dir must be absolute")
	}
	for p := filepath.Clean(*state); ; p = filepath.Dir(p) {
		if info, e := os.Lstat(p); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("state-dir must not contain symlinks")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if e = os.MkdirAll(*state, 0700); e != nil {
		return e
	}
	info, e := os.Stat(*state)
	if e != nil || info.Mode().Perm()&0077 != 0 {
		return errors.New("state-dir must be private (0700)")
	}
	lock, e := os.OpenFile(filepath.Join(*state, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return errors.New("another agent uses this state directory")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if *ca != "" {
		b, e := os.ReadFile(*ca)
		if e != nil {
			return errors.New("cannot read CA file")
		}
		roots, e := x509.SystemCertPool()
		if e != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(b) {
			return errors.New("invalid CA file")
		}
		tlsConfig.RootCAs = roots
	}
	c := &client{url: strings.TrimRight(*central, "/"), http: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects are not permitted") }}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	path := filepath.Join(*state, "identity.json")
	b, e := privateFile(path)
	if e == nil {
		if json.Unmarshal(b, &c.identity) != nil || c.identity.URL != c.url || len(c.identity.ID) != 64 || len(c.identity.Credential) != 64 {
			return errors.New("invalid identity or changed central URL; explicit re-enrollment required")
		}
	} else if os.IsNotExist(e) {
		b, e := privateFile(*token)
		if e != nil {
			return errors.New("first enrollment requires a private enrollment-file")
		}
		var out identity
		if e = c.post(ctx, "enroll", map[string]any{"token": strings.TrimSpace(string(b)), "protocol": agents.Protocol}, &out); e != nil {
			return e
		}
		if len(out.ID) != 64 || len(out.Credential) != 64 {
			return errors.New("invalid enrollment response")
		}
		out.URL = c.url
		if e = save(path, out); e != nil {
			return errors.New("cannot save enrolled identity; revoke the host centrally before enrolling again")
		}
		c.identity = out
		log.Print("Agent enrolled; remove the enrollment secret mount/file after first start")
	} else {
		return e
	}
	backend, e := app.NewAgentBackend(*config, filepath.Join(*state, "managed"), *socket, *updateDir)
	if e != nil {
		return e
	}
	defer backend.Close()
	capabilities := []string{agents.ControlCapability, agents.BuildsCapability, agents.ImagesCapability, agents.VolumesCapability, agents.NetworksCapability, agents.ImageBatchCapability}
	if backend.HasActionEvents() {
		capabilities = append(capabilities, agents.EventsCapability)
	}
	var updateStore *agentupdate.Store
	if *updateDir != "" {
		updateStore, e = agentupdate.Open(*updateDir)
		if e != nil {
			return e
		}
		capabilities = append(capabilities, agents.UpdatesCapability)
	}
	dockerClient := docker.NewClient(*socket)
	backoff := time.Second
	for ctx.Err() == nil {
		var connected struct {
			Generation string `json:"generation"`
			Events     bool   `json:"events"`
		}
		e = c.post(ctx, "connect", map[string]any{"protocol": agents.Protocol, "version": buildinfo.Version, "capabilities": capabilities}, &connected)
		if e == nil {
			if updateStore != nil {
				if e = updateStore.Write("connection.json", agentupdate.Connection{Version: buildinfo.Version, Revision: buildinfo.Revision, At: time.Now()}); e != nil {
					return e
				}
			}
			log.Print("Agent connected (logs, consoles and project management)")
			connection, stopConnection := context.WithCancel(ctx)
			controlDone := make(chan error, 1)
			go func() { controlDone <- c.control(connection, backend, connected.Generation); stopConnection() }()
			sequence := uint64(0)
			for connection.Err() == nil {
				rows, err := dockerClient.List(connection)
				sequence++
				update := agents.Update{Protocol: agents.Protocol, Generation: connected.Generation, Sequence: sequence, Containers: rows}
				if err != nil {
					update.Containers = nil
					update.Error = "Local Docker inventory unavailable"
				}
				if encoded, _ := json.Marshal(update); len(encoded) > agents.MaxPayload || len(rows) > 10000 {
					update.Containers = nil
					update.Error = "Local Docker inventory exceeds protocol limits"
				}
				e = c.post(connection, "inventory", update, nil)
				if e != nil {
					break
				}
				if connected.Events {
					events := backend.ActionEvents()
					if len(events) > 0 {
						var result struct {
							Ack []string `json:"ack"`
						}
						if err := c.post(connection, "events", map[string]any{"generation": connected.Generation, "events": events}, &result); err == nil {
							if err = backend.AckActionEvents(result.Ack); err != nil {
								log.Print("Cannot persist action event acknowledgement")
							}
						}
					}
				}
				backoff = time.Second
				select {
				case <-connection.Done():
					break
				case <-time.After(5 * time.Second):
				}
			}
			stopConnection()
			controlErr := <-controlDone
			if e == nil {
				e = controlErr
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		log.Print(e)
		delay := backoff + time.Duration(rand.Int64N(int64(backoff/2)+1))
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		if backoff < 20*time.Second {
			backoff *= 2
		}
	}
	return nil
}
func main() {
	if e := run(); e != nil {
		log.Fatal(e)
	}
}
