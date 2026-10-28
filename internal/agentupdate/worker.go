package agentupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
)

type Runner func(context.Context, []string) ([]byte, error)
type Worker struct {
	Store        *Store
	Installation string
	Run          Runner
	Poll         time.Duration
}
type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		return 0, errors.New("Docker response exceeds update limit")
	}
	return b.Buffer.Write(p)
}
func DockerRunner(socket string) Runner {
	return func(ctx context.Context, args []string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "DOCKER_HOST=unix://" + socket, "LANG=C", "LC_ALL=C", "LANGUAGE=en", "COMPOSE_ANSI=never", "COMPOSE_PROGRESS=plain"}
		var out limitedOutput
		cmd.Stdout, cmd.Stderr = &out, io.Discard
		err := cmd.Run()
		return out.Bytes(), err
	}
}
func (w *Worker) phase(o *Operation, phase, message string) error {
	o.Phase, o.Message, o.Updated = phase, message, time.Now()
	return w.Store.Write("operation.json", o)
}
func (w *Worker) Serve(ctx context.Context) error {
	if w.Poll <= 0 {
		w.Poll = 2 * time.Second
	}
	if !filepath.IsAbs(w.Installation) || filepath.Base(w.Installation) != "reef-agent" {
		return errors.New("Updater requires the dedicated absolute reef-agent installation")
	}
	if _, err := privateInput(w.Installation, "compose.yaml", false); err != nil {
		return err
	}
	if _, err := privateInput(w.Installation, ".env", true); err != nil {
		return err
	}
	unlock, err := w.Store.Lock("worker.lock")
	if err != nil {
		return err
	}
	defer unlock()
	gate, err := w.Store.Lock("request.lock")
	if err != nil {
		return err
	}
	o, err := w.Store.Operation()
	if err == nil && o.Running() {
		err = w.phase(&o, "unknown", "Updater restarted; inspect the agent and configuration before retrying. No request was replayed.")
	}
	gate()
	if err != nil {
		return err
	}
	beatCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_ = w.Store.Write("heartbeat.json", Heartbeat{At: time.Now()})
			select {
			case <-beatCtx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	defer func() { cancel(); <-done; _ = w.Store.Write("heartbeat.json", Heartbeat{}) }()
	for ctx.Err() == nil {
		gate, err := w.Store.Lock("request.lock")
		if err == nil {
			o, err := w.Store.Operation()
			if err == nil && o.Phase == "queued" {
				job, stop := context.WithTimeout(ctx, 10*time.Minute)
				err = w.execute(job, &o)
				stop()
				if err != nil {
					phase := "failed"
					if o.Phase == "recreating" || o.Phase == "reconnecting" {
						phase = "unknown"
					}
					if saveErr := w.phase(&o, phase, err.Error()); saveErr != nil {
						gate()
						return saveErr
					}
				}
			}
			gate()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(w.Poll):
		}
	}
	return nil
}
func privateInput(root, name string, private bool) ([]byte, error) {
	for p := filepath.Clean(root); ; p = filepath.Dir(p) {
		if info, err := os.Lstat(p); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("Installation paths must be existing directories without symlinks")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	p := filepath.Join(root, name)
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 || (private && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("Update requires regular compose.yaml and private .env files without symlinks")
	}
	return os.ReadFile(p)
}
func envValue(b []byte, key string) (string, error) {
	value, count := "", 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+"=") {
			if !strings.HasPrefix(line, key+"=") {
				return "", errors.New("Use plain, unquoted agent image and project settings in .env")
			}
			value = strings.TrimPrefix(line, key+"=")
			count++
		}
	}
	if count != 1 || value == "" {
		return "", errors.New("Update requires one explicit agent image and Compose project name in .env")
	}
	return value, nil
}

var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,99}$`)
var containerID = regexp.MustCompile(`^[0-9a-f]{64}$`)
var operationID = regexp.MustCompile(`^[0-9a-f]{48}$`)

type composeModel struct {
	Name     string                     `json:"name"`
	Services map[string]json.RawMessage `json:"services"`
	Volumes  map[string]struct {
		Name string `json:"name"`
	} `json:"volumes"`
}
type agentService struct {
	Image   string                                  `json:"image"`
	Build   json.RawMessage                         `json:"build"`
	Volumes []struct{ Type, Source, Target string } `json:"volumes"`
}
type containerState struct {
	ID     string `json:"Id"`
	Image  string `json:"Image"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
	Mounts []struct{ Type, Name, Destination string } `json:"Mounts"`
}

func (w *Worker) inspect(ctx context.Context, id string) (containerState, error) {
	var rows []containerState
	b, err := w.Run(ctx, []string{"container", "inspect", id})
	if err != nil || json.Unmarshal(b, &rows) != nil || len(rows) != 1 {
		return containerState{}, errors.New("Cannot inspect the existing agent container")
	}
	return rows[0], nil
}
func (w *Worker) execute(ctx context.Context, o *Operation) error {
	if !ValidTarget(o.Version, o.Revision) || !Newer(o.Version, o.FromVersion) || !operationID.MatchString(o.ID) || time.Since(o.Started) < 0 || time.Since(o.Started) > time.Minute {
		return errors.New("Invalid or expired agent update request")
	}
	env, err := privateInput(w.Installation, ".env", true)
	if err != nil {
		return err
	}
	compose, err := privateInput(w.Installation, "compose.yaml", false)
	if err != nil {
		return err
	}
	overlay, err := privateInput(w.Installation, "compose.updates.yaml", false)
	if err != nil {
		return err
	}
	old, err := envValue(env, "REEF_AGENT_IMAGE")
	if err != nil {
		return err
	}
	if old != Repository+":v"+o.FromVersion {
		return errors.New("Agent image differs from the running version; review the installation manually")
	}
	project, err := envValue(env, "COMPOSE_PROJECT_NAME")
	if err != nil || !projectName.MatchString(project) {
		return errors.New("Invalid explicit Compose project name")
	}
	base := []string{"compose", "--project-directory", w.Installation, "--env-file", filepath.Join(w.Installation, ".env"), "--project-name", project, "-f", filepath.Join(w.Installation, "compose.yaml"), "-f", filepath.Join(w.Installation, "compose.updates.yaml")}
	command := func(args ...string) ([]byte, error) { return w.Run(ctx, append(append([]string{}, base...), args...)) }
	config, err := command("config", "--format", "json")
	var model composeModel
	var service agentService
	if err != nil || json.Unmarshal(config, &model) != nil || model.Name != project || json.Unmarshal(model.Services["reef-agent"], &service) != nil || service.Image != old || len(service.Build) > 0 {
		return errors.New("Unsupported agent Compose configuration")
	}
	stateVolume := model.Volumes["agent-state"].Name
	validMount := false
	for _, mount := range service.Volumes {
		if mount.Target == "/var/lib/reef-agent" && mount.Type == "volume" && mount.Source == "agent-state" {
			validMount = true
		}
	}
	if !validMount || stateVolume == "" {
		return errors.New("The existing named agent-state identity volume is required")
	}
	getContainer := func() (containerState, error) {
		b, err := command("ps", "--all", "--quiet", "--no-trunc", "reef-agent")
		id := strings.TrimSpace(string(b))
		if err != nil || !containerID.MatchString(id) {
			return containerState{}, errors.New("Exactly one existing agent container is required")
		}
		c, err := w.inspect(ctx, id)
		if err != nil {
			return c, err
		}
		if c.Config.Labels["com.docker.compose.project"] != project || c.Config.Labels["com.docker.compose.service"] != "reef-agent" {
			return c, errors.New("Agent Compose identity changed")
		}
		for _, mount := range c.Mounts {
			if mount.Type == "volume" && mount.Name == stateVolume && mount.Destination == "/var/lib/reef-agent" {
				return c, nil
			}
		}
		return c, errors.New("Agent identity volume changed")
	}
	before, err := getContainer()
	if err != nil {
		return err
	}
	if !before.State.Running || before.Config.Labels["org.opencontainers.image.version"] != o.FromVersion {
		return errors.New("Existing agent version or running state changed")
	}
	if err = w.phase(o, "pulling", "Downloading and checking the matching agent image."); err != nil {
		return err
	}
	target := Repository + ":v" + o.Version
	if _, err = w.Run(ctx, []string{"pull", target}); err != nil {
		return errors.New("Agent image download failed; the existing agent was preserved. Check registry access on this host.")
	}
	b, err := w.Run(ctx, []string{"image", "inspect", target})
	var images []struct {
		ID     string `json:"Id"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err != nil || json.Unmarshal(b, &images) != nil || len(images) != 1 || images[0].Config.Labels["org.opencontainers.image.version"] != o.Version || images[0].Config.Labels["org.opencontainers.image.revision"] != o.Revision || !strings.HasPrefix(images[0].ID, "sha256:") || !containerID.MatchString(strings.TrimPrefix(images[0].ID, "sha256:")) {
		return errors.New("Published agent image does not match the central version and revision; existing agent preserved")
	}
	// Refuse changes made by another operator during the download.
	currentEnv, err := privateInput(w.Installation, ".env", true)
	if err != nil {
		return err
	}
	currentCompose, err := privateInput(w.Installation, "compose.yaml", false)
	if err != nil {
		return err
	}
	currentOverlay, err := privateInput(w.Installation, "compose.updates.yaml", false)
	if err != nil {
		return err
	}
	nowConfig, err := command("config", "--format", "json")
	var a, bconfig any
	if err != nil || json.Unmarshal(config, &a) != nil || json.Unmarshal(nowConfig, &bconfig) != nil || !reflect.DeepEqual(a, bconfig) || sha256.Sum256(env) != sha256.Sum256(currentEnv) || sha256.Sum256(compose) != sha256.Sum256(currentCompose) || sha256.Sum256(overlay) != sha256.Sum256(currentOverlay) {
		return errors.New("Agent configuration changed; refresh before retrying")
	}
	current, err := getContainer()
	if err != nil || !current.State.Running || current.ID != before.ID || current.Image != before.Image {
		return errors.New("Agent container changed; refresh before retrying")
	}
	lines := strings.Split(string(env), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "REEF_AGENT_IMAGE=") {
			lines[i] = "REEF_AGENT_IMAGE=" + target
		}
	}
	// Atomic .env replacement preserves every other private setting.
	if err = replaceEnv(w.Installation, []byte(strings.Join(lines, "\n"))); err != nil {
		return errors.New("Cannot persist the requested agent image; existing agent preserved")
	}
	if err = w.phase(o, "recreating", "Recreating only reef-agent; identity and configuration are preserved."); err != nil {
		return err
	}
	if _, err = command("up", "-d", "--no-deps", "--no-build", "--pull", "never", "reef-agent"); err != nil {
		return errors.New("Agent recreation outcome unknown. Inspect this host before retrying; no rollback was attempted.")
	}
	if err = w.phase(o, "reconnecting", "Waiting for the updated agent to reconnect to central Reef."); err != nil {
		return err
	}
	for {
		var connection Connection
		if w.Store.Read("connection.json", &connection) == nil && connection.Version == o.Version && connection.Revision == o.Revision && connection.At.After(o.Started) {
			current, err := getContainer()
			if err == nil && current.State.Running && current.Image == images[0].ID {
				return w.phase(o, "completed", "Agent updated and reconnected with the expected version and revision.")
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("Agent reconnection was not confirmed. Inspect this host; the requested image remains configured and no rollback was attempted.")
		case <-time.After(time.Second):
		}
	}
}
func replaceEnv(root string, content []byte) error {
	f, err := os.CreateTemp(root, ".agent-update-env-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(content); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(root, ".env"))
	}
	if err != nil {
		return err
	}
	d, err := os.Open(root)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
