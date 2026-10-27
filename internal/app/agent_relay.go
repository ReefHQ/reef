package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reef/internal/agents"
	"reef/internal/docker"
	"strconv"
	"sync"
	"time"
)

func (s *Server) remoteReady(host string) bool {
	return s.cfg.AgentsEnabled && s.agents != nil && s.relay != nil && s.agents.CanControl(host, "")
}
func (s *Server) remotePeer(r *http.Request) (*remoteDocker, error) {
	host := r.URL.Query().Get("host")
	sess, ok := s.getSession(r)
	if !ok || !s.remoteReady(host) {
		return nil, errors.New("Remote host is disconnected or needs an agent upgrade")
	}
	sum := sha256.Sum256([]byte(host + ":" + sess.CSRF))
	return &remoteDocker{server: s, host: host, owner: hex.EncodeToString(sum[:]), expires: sess.Expires}, nil
}
func (s *Server) discoveryFor(r *http.Request) (docker.Lister, error) {
	if host := r.URL.Query().Get("host"); host != "" && host != "local" {
		return s.remotePeer(r)
	}
	return s.discovery, nil
}
func (s *Server) remoteHTTP(w http.ResponseWriter, r *http.Request, sess session, host string) {
	peer, err := s.remotePeer(r)
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	response, err := peer.do(ctx, r.Method, r.URL.Path, r.URL.Query(), r.PostForm)
	if err != nil {
		http.Error(w, agents.ErrRelay.Error(), 503)
		return
	}
	defer response.Body.Close()
	if response.StatusCode == 401 {
		http.Error(w, "Remote session rejected; refresh host status", 409)
		return
	}
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	if field := response.Header.Get("X-Reef-Field"); field != "" {
		w.Header().Set("X-Reef-Field", field)
	}
	if r.URL.Path == "/api/containers" && response.StatusCode == 200 {
		data, err := io.ReadAll(io.LimitReader(response.Body, agents.MaxPayload+1))
		var result map[string]json.RawMessage
		if err != nil || len(data) > agents.MaxPayload || json.Unmarshal(data, &result) != nil {
			http.Error(w, "Invalid remote inventory response", 503)
			return
		}
		view, _, ok := s.agents.Inventory(host)
		if !ok {
			http.Error(w, "Remote host not found", 404)
			return
		}
		result["host"], _ = json.Marshal(view)
		writeJSON(w, result)
		return
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(response.Body, agents.MaxPayload+1))
}

type remoteDocker struct {
	server      *Server
	host, owner string
	expires     time.Time
}

func (p *remoteDocker) do(ctx context.Context, method, path string, query, form url.Values) (*http.Response, error) {
	if !p.server.remoteReady(p.host) {
		return nil, agents.ErrRelay
	}
	q := url.Values{}
	for key, values := range query {
		if key != "host" {
			q[key] = values
		}
	}
	f := url.Values{}
	for key, values := range form {
		if key != "csrf" {
			f[key] = values
		}
	}
	return p.server.relay.Do(ctx, p.host, agents.Command{Owner: p.owner, Expires: p.expires, Method: method, Path: path, Query: q, Form: f})
}
func (p *remoteDocker) List(ctx context.Context) ([]docker.Container, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, err := p.do(ctx, "GET", "/api/containers", nil, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, agents.ErrRelay
	}
	var result struct{ Containers []docker.Container }
	if err = json.NewDecoder(io.LimitReader(response.Body, agents.MaxPayload)).Decode(&result); err != nil || len(result.Containers) > 10000 {
		return nil, agents.ErrRelay
	}
	for _, row := range result.Containers {
		if !docker.ValidContainerID(row.ID) {
			return nil, agents.ErrRelay
		}
	}
	return result.Containers, nil
}
func (p *remoteDocker) ReadLogs(ctx context.Context, id, stream string, tail int) (docker.Logs, error) {
	var logs docker.Logs
	response, err := p.do(ctx, "GET", "/api/container-logs", url.Values{"id": {id}, "stream": {stream}, "tail": {strconv.Itoa(tail)}}, nil)
	if err != nil {
		return logs, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return logs, errors.New("Remote container logs are unavailable")
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 6*docker.MaxLogBytes+16384)).Decode(&logs)
	if len(logs.Text) > 3*docker.MaxLogBytes {
		return docker.Logs{}, agents.ErrRelay
	}
	return logs, err
}
func (p *remoteDocker) OpenConsole(ctx context.Context, id, shell string, cols, rows int) (docker.Console, error) {
	response, err := p.do(ctx, "POST", "/api/console/open", nil, url.Values{"id": {id}, "shell": {shell}, "cols": {strconv.Itoa(cols)}, "rows": {strconv.Itoa(rows)}, "confirmed": {"yes"}})
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return nil, errors.New("Remote console could not be opened")
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 65536)
	terminal := &remoteConsole{peer: p, ctx: ctx, body: response.Body, scanner: scanner}
	timer := time.AfterFunc(12*time.Second, func() { response.Body.Close() })
	defer timer.Stop()
	if !scanner.Scan() {
		response.Body.Close()
		return nil, agents.ErrRelay
	}
	var ready struct{ Type, ID string }
	if json.Unmarshal(scanner.Bytes(), &ready) != nil || ready.Type != "ready" || !identifier.MatchString(ready.ID) || len(ready.ID) > 80 {
		response.Body.Close()
		return nil, errors.New("Invalid remote console response")
	}
	terminal.id = ready.ID
	return terminal, nil
}

type remoteConsole struct {
	peer    *remoteDocker
	ctx     context.Context
	id      string
	body    io.ReadCloser
	scanner *bufio.Scanner
	pending bytes.Buffer
	once    sync.Once
}

func (t *remoteConsole) Read(p []byte) (int, error) {
	for t.pending.Len() == 0 {
		if !t.scanner.Scan() {
			if err := t.scanner.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		var frame struct{ Type, Data string }
		if json.Unmarshal(t.scanner.Bytes(), &frame) != nil {
			return 0, agents.ErrRelay
		}
		if frame.Type == "closed" {
			return 0, io.EOF
		}
		if frame.Type == "output" {
			raw, err := base64.StdEncoding.DecodeString(frame.Data)
			if err != nil || len(raw) > 4096 {
				return 0, agents.ErrRelay
			}
			t.pending.Write(raw)
		}
	}
	return t.pending.Read(p)
}
func (t *remoteConsole) command(ctx context.Context, path string, form url.Values) error {
	form.Set("console", t.id)
	response, err := t.peer.do(ctx, "POST", "/api/console/"+path, nil, form)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != 200 && response.StatusCode != 204 {
		return agents.ErrRelay
	}
	return nil
}
func (t *remoteConsole) Write(p []byte) (int, error) {
	ctx, cancel := context.WithTimeout(t.ctx, 10*time.Second)
	defer cancel()
	if err := t.command(ctx, "input", url.Values{"data": {base64.StdEncoding.EncodeToString(p)}}); err != nil {
		return 0, err
	}
	return len(p), nil
}
func (t *remoteConsole) Resize(ctx context.Context, cols, rows int) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return t.command(ctx, "resize", url.Values{"cols": {strconv.Itoa(cols)}, "rows": {strconv.Itoa(rows)}})
}
func (t *remoteConsole) Close() error { t.once.Do(func() { t.body.Close() }); return nil }
