// Package local bootstraps Reef against a selected local Linux Docker engine.
package local

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

type command func(...string) ([]byte, error)
type Launcher struct {
	addresses func() ([]net.IP, error)
	root      string
	run       command
	getenv    func(string) string
	platform  string
	daemonVM  bool
	out       io.Writer
}
type metadata struct {
	Prefix   string `json:"prefix"`
	Image    string `json:"image"`
	Port     int    `json:"ui_port"`
	Context  string `json:"context"`
	Endpoint string `json:"endpoint"`
	EngineID string `json:"engine_id"`
}
type engine struct{ Context, Endpoint, ID string }

func New(root string) (*Launcher, error) {
	root, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return nil, e
	}
	return &Launcher{addresses: hostAddresses, root: root, platform: runtime.GOOS, getenv: os.Getenv, out: os.Stdout, run: func(args ...string) ([]byte, error) {
		cmd := exec.Command("docker", args...)
		cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C", "LANGUAGE=en")
		b, e := cmd.CombinedOutput()
		if e != nil {
			return nil, fmt.Errorf("Docker command failed: %w\n%s", e, b)
		}
		return b, nil
	}}, nil
}
func (l *Launcher) runtime() string { return filepath.Join(l.root, ".reef-local") }
func (l *Launcher) localEngine() (engine, error) {
	var result engine
	if l.platform != "linux" && l.platform != "darwin" {
		return result, errors.New("The local launcher supports Linux and macOS only.")
	}
	if l.getenv("DOCKER_HOST") != "" || l.getenv("DOCKER_CONTEXT") != "" {
		return result, errors.New("Unset DOCKER_HOST and DOCKER_CONTEXT; select a supported local Docker context explicitly.")
	}
	b, e := l.run("context", "show")
	if e != nil {
		return result, e
	}
	result.Context = strings.TrimSpace(string(b))
	if result.Context == "" {
		return result, errors.New("Cannot read the selected Docker context.")
	}
	b, e = l.run("context", "inspect", result.Context)
	if e != nil {
		return result, e
	}
	var contexts []struct {
		Endpoints struct{ Docker struct{ Host string } }
	}
	if e = json.Unmarshal(b, &contexts); e != nil || len(contexts) != 1 {
		return result, errors.New("Cannot read the selected Docker context.")
	}
	result.Endpoint = contexts[0].Endpoints.Docker.Host
	endpoint, e := url.Parse(result.Endpoint)
	if e != nil || endpoint.Scheme != "unix" || endpoint.Host != "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path == "/" || !filepath.IsAbs(endpoint.Path) || filepath.Clean(endpoint.Path) != endpoint.Path {
		return result, errors.New("Refusing a non-local Docker endpoint. Select a local Unix-socket context.")
	}
	b, e = l.run("--context", result.Context, "info", "--format", "{{json .}}")
	if e != nil {
		return result, e
	}
	var info struct{ ID, Name, OperatingSystem, OSType string }
	if e = json.Unmarshal(b, &info); e != nil {
		return result, e
	}
	if info.ID == "" || info.OSType != "linux" {
		return result, errors.New("A reachable Linux Docker engine with a stable ID is required.")
	}
	// A VM-backed engine exposes its own socket inside the Linux VM.
	l.daemonVM = l.platform == "darwin" || info.OperatingSystem == "Docker Desktop"
	result.ID = info.ID
	return result, nil
}
func (l *Launcher) socketSource(current engine) string {
	if l.platform == "darwin" || l.daemonVM {
		return "/var/run/docker.sock"
	}
	endpoint, _ := url.Parse(current.Endpoint)
	return endpoint.Path
}

func validEngine(meta metadata, current engine) error {
	if meta.Context != current.Context || meta.Endpoint != current.Endpoint || meta.EngineID != current.ID {
		return errors.New("Docker engine/context differs from this Reef installation; refusing to operate.")
	}
	if !regexp.MustCompile(`^reef-[a-z0-9-]+$`).MatchString(meta.Prefix) || meta.Image != "reef:"+meta.Prefix || meta.Port < 1 || meta.Port > 65535 {
		return errors.New("Invalid local Reef metadata; inspect it before proceeding.")
	}
	return nil
}
func writeNew(path string, data []byte) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(data)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return e
}
func writeJSON(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	return writeNew(path, append(b, '\n'))
}
func (l *Launcher) initialize(current engine, modes ...string) (metadata, error) {
	var meta metadata
	if filepath.Dir(l.root) == string(filepath.Separator) {
		return meta, errors.New("Reef's installation parent cannot be the filesystem root")
	}
	dir := l.runtime()
	if e := os.MkdirAll(dir, 0700); e != nil {
		return meta, e
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return meta, e
	}
	for _, entry := range entries {
		if entry.Name() != "tools" || !entry.IsDir() {
			return meta, errors.New("Existing .reef-local content without metadata; refusing to overwrite it.")
		}
	}
	b := make([]byte, 16)
	if _, e = rand.Read(b); e != nil {
		return meta, e
	}
	prefix := "reef-local-" + hex.EncodeToString(b[:5])
	address := "127.0.0.1"
	if len(modes) > 0 && modes[0] == "lan" {
		address = "0.0.0.0"
	}
	listener, e := net.Listen("tcp", net.JoinHostPort(address, "8099"))
	if e != nil {
		listener, e = net.Listen("tcp", net.JoinHostPort(address, "0"))
	}
	if e != nil {
		return meta, e
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	meta = metadata{prefix, "reef:" + prefix, port, current.Context, current.Endpoint, current.ID}
	password := make([]byte, 24)
	if _, e = rand.Read(password); e != nil {
		return meta, e
	}
	if e = writeNew(filepath.Join(dir, "password.txt"), []byte(hex.EncodeToString(password)+"\n")); e != nil {
		return meta, e
	}
	if e = os.MkdirAll(filepath.Join(dir, "state"), 0700); e != nil {
		return meta, e
	}
	if e = writeJSON(filepath.Join(dir, "config.json"), map[string]any{"listen": "0.0.0.0:8099", "state_dir": "/var/lib/reef", "secure_cookie": false, "projects": []any{}}); e != nil {
		return meta, e
	}
	bind := func(source, target string, ro bool) map[string]any {
		return map[string]any{"type": "bind", "source": source, "target": target, "read_only": ro, "bind": map[string]any{"create_host_path": false}}
	}
	install := map[string]any{"services": map[string]any{"reef": map[string]any{
		"image": meta.Image, "build": map[string]string{"context": l.root}, "labels": map[string]string{"io.reef.local": meta.Prefix},
		"ports": []string{fmt.Sprintf("%s:%d:8099", address, meta.Port)}, "environment": map[string]string{"QCU_PASSWORD_FILE": "/run/secrets/reef_password", "REEF_INSTALLATION_DIR": l.root}, "secrets": []string{"reef_password"}, "stop_grace_period": "46m", "restart": "unless-stopped",
		"volumes":   []any{bind(filepath.Dir(l.root), filepath.Dir(l.root), false), bind(filepath.Join(dir, "state"), "/var/lib/reef", false), bind(filepath.Join(dir, "config.json"), "/etc/reef/config.json", true), bind(l.socketSource(current), "/var/run/docker.sock", false)},
		"read_only": true, "tmpfs": []string{"/tmp"}, "security_opt": []string{"no-new-privileges:true"},
	}}, "secrets": map[string]any{"reef_password": map[string]string{"file": filepath.Join(dir, "password.txt")}}}
	if e = writeJSON(filepath.Join(dir, "compose.json"), install); e != nil {
		return meta, e
	}
	if e = writeJSON(filepath.Join(dir, "local.json"), meta); e != nil {
		return meta, e
	}
	return meta, nil
}
func (l *Launcher) Run(action string, options ...string) error {
	recoverUser := ""
	if action == "recover-admin" {
		if len(options) != 1 || !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`).MatchString(options[0]) {
			return errors.New(launcherUsage)
		}
		recoverUser = options[0]
		options = nil
	}

	mode, err := networkMode(action, options)
	if err != nil {
		return err
	}
	switch action {
	case "up", "stop", "status", "password", "recover-admin":
	default:
		return errors.New(launcherUsage)
	}
	if action == "password" {
		b, e := os.ReadFile(filepath.Join(l.runtime(), "password.txt"))
		if e != nil {
			return errors.New("No local password. Run ./reef-local.sh up first.")
		}
		_, e = l.out.Write(b)
		return e
	}
	current, e := l.localEngine()
	if e != nil {
		return e
	}
	var meta metadata
	b, e := os.ReadFile(filepath.Join(l.runtime(), "local.json"))
	if e == nil {
		if e = json.Unmarshal(b, &meta); e != nil {
			return e
		}
	} else if os.IsNotExist(e) && action == "up" {
		meta, e = l.initialize(current, mode)
		if e != nil {
			return e
		}
	} else {
		return errors.New("No local Reef installation. Run ./reef-local.sh up first.")
	}
	if e = validEngine(meta, current); e != nil {
		return e
	}
	compose := []string{"--context", meta.Context, "compose", "--project-name", meta.Prefix + "-ui", "-f", filepath.Join(l.runtime(), "compose.json")}
	invoke := func(args ...string) error {
		all := append(append([]string{}, compose...), args...)
		out, e := l.run(all...)
		if len(out) > 0 {
			l.out.Write(out)
		}
		return e
	}
	if action == "recover-admin" {
		return invoke("run", "--rm", "--no-deps", "reef", "-config", "/etc/reef/config.json", "-recover-admin", recoverUser)
	}
	switch action {
	case "up":
		if e = l.ensureInstallationRoot(); e != nil {
			return e
		}
		if e = l.ensureRestartPolicy(); e != nil {
			return e
		}
		if e = l.ensureNetworkMode(mode); e != nil {
			return e
		}
		if e = invoke("config", "--quiet"); e == nil {
			e = invoke("up", "-d", "--build")
		}
	case "stop":
		e = invoke("stop")
	case "status":
		e = invoke("ps")
	}
	if e != nil {
		return e
	}
	if action == "stop" {
		fmt.Fprintln(l.out, "Reef stopped. Managed projects are not stopped by this command.")
	} else {
		l.printAccess(compose)
	}
	return nil
}
