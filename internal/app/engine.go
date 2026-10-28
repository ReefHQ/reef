package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Executor interface {
	Run(context.Context, Project, []string, io.Writer) error
}
type DockerExecutor struct{ Socket string }

func (d DockerExecutor) Run(ctx context.Context, p Project, args []string, w io.Writer) error {
	return dockerCommand(ctx, p, args, w, w, d.Socket)
}

// Output keeps diagnostic stderr separate from machine-readable JSON/IDs.
func (d DockerExecutor) Output(ctx context.Context, p Project, args []string, w io.Writer) error {
	return dockerCommand(ctx, p, args, w, io.Discard, d.Socket)
}
func dockerCommand(ctx context.Context, p Project, args []string, stdout, stderr io.Writer, socket string) error {
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = p.Directory
	// Do not leak the UI password or inherit Compose/Docker overrides from the server.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "DOCKER_HOST=unix://" + socket, "COMPOSE_ANSI=never", "COMPOSE_PROGRESS=plain", "LANG=C", "LC_ALL=C", "LANGUAGE=en"}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

type Operation struct {
	Build      BuildOptions `json:"build,omitempty"`
	ID         string       `json:"id"`
	Action     string       `json:"action"`
	Phase      string       `json:"phase"`
	Running    bool         `json:"running"`
	Result     string       `json:"result"`
	Log        string       `json:"log"`
	Started    time.Time    `json:"started"`
	Finished   time.Time    `json:"finished,omitempty"`
	NeedsStart bool         `json:"needs_start"`
}
type Managed struct {
	Project      Project
	mu           sync.Mutex
	Op           Operation
	busy         bool
	persistError string
	ImageUpdates ImageUpdates
}
type Manager struct {
	mu            sync.RWMutex
	config        Config
	registered    []Project
	registryHash  string
	Projects      map[string]*Managed
	Order         []string
	StateDir      string
	Exec          Executor
	wg            sync.WaitGroup
	agentUpdating bool // Protected by mu; host maintenance excludes checks as well.
	storageBusy   bool // Protected by mu; excludes image/volume mutations and Compose work.
	Images        *ImageService
	imageCheckMu  sync.Mutex
	imagesContext context.Context
}

func newManager(c Config, x Executor) (*Manager, error) {
	if e := os.MkdirAll(c.StateDir, 0700); e != nil {
		return nil, e
	}
	m := &Manager{Projects: map[string]*Managed{}, StateDir: c.StateDir, Exec: x, config: c}
	extra, e := readRegistrations(c)
	if e != nil {
		return nil, e
	}
	m.registered = extra
	raw, e := registryBytes(c)
	if e != nil {
		return nil, e
	}
	m.registryHash = digest(raw)
	for _, p := range append(append([]Project{}, c.Projects...), extra...) {
		mp := &Managed{Project: p}
		m.Projects[p.ID] = mp
		m.Order = append(m.Order, p.ID)
		b, e := os.ReadFile(filepath.Join(c.StateDir, p.ID+".json"))
		if e == nil {
			if e = json.Unmarshal(b, &mp.Op); e != nil {
				return nil, e
			}
			if mp.Op.Running {
				mp.Op.Running = false
				mp.Op.Result = "Interrupted by a server restart: Docker state is unknown. Check the project, then start it if needed."
				mp.Op.Phase = "interrupted"
				if mp.Op.Action != "build" {
					mp.Op.NeedsStart = true
				} else {
					mp.Op.Result = "Build interrupted by a server restart: image state is unknown. Review it before retrying."
				}
				mp.Op.Finished = time.Now()
				if e = m.persist(mp); e != nil {
					return nil, e
				}
				if mp.Op.Action == "build" {
					raw, _ := json.Marshal(mp.Op)
					if e = atomicWrite(filepath.Join(m.StateDir, p.ID+"-"+mp.Op.ID+".history.json"), raw, 0600); e != nil {
						return nil, e
					}
					if e = m.writeBuildSummary(p.ID, mp.Op); e != nil {
						return nil, e
					}
				}
			}
		} else if !os.IsNotExist(e) {
			return nil, e
		}
	}
	return m, nil
}
func randomID() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".qcu-write-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if old, statErr := os.Stat(path); statErr == nil {
		if stat, ok := old.Sys().(*syscall.Stat_t); ok {
			if e = f.Chown(int(stat.Uid), int(stat.Gid)); e != nil {
				f.Close()
				return fmt.Errorf("preserving file ownership : %w", e)
			}
		}
	}
	if e = f.Chmod(mode.Perm()); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (m *Manager) persist(p *Managed) error {
	b, e := json.MarshalIndent(p.Op, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(filepath.Join(m.StateDir, p.Project.ID+".json"), b, 0600)
}
func (m *Manager) update(p *Managed, fn func(*Operation)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(&p.Op)
	e := m.persist(p)
	if e != nil {
		p.persistError = "Cannot write operation log : " + e.Error()
	}
	return e
}
func (m *Manager) begin(id, action, rev, content string) error {
	return m.beginOptions(id, action, rev, content, BuildOptions{})
}
func (m *Manager) beginOptions(id, action, rev, content string, opts BuildOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storageBusy {
		return errors.New("an image or volume operation is already running")
	}
	p, ok := m.Projects[id]
	if !ok {
		return errors.New("unknown project")
	}
	switch action {
	case "save", "apply", "start", "stop", "build":
	default:
		return errors.New("action not allowed")
	}
	if action == "build" {
		for _, other := range m.Projects {
			other.mu.Lock()
			busy := other.busy || other.ImageUpdates.Checking
			other.mu.Unlock()
			if busy {
				return errors.New("Wait for project operations and image checks before building")
			}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy || p.ImageUpdates.Checking {
		return errors.New("an operation is already running")
	}
	current, e := revision(p.Project)
	if e != nil {
		return e
	}
	if current != rev {
		return errors.New("outdated edit: reload the file (copy your changes first)")
	}
	if action == "save" || action == "apply" {
		if len(content) == 0 || len(content) > 1024*1024 {
			return errors.New("file is empty or exceeds 1 MiB")
		}
	}
	previousOp := p.Op
	p.persistError = ""
	prior := p.Op.NeedsStart
	p.Op = Operation{Build: opts, ID: randomID(), Action: action, Phase: "queued", Running: true, Started: time.Now(), NeedsStart: prior}
	if e = m.persist(p); e != nil {
		p.Op = previousOp
		return e
	}
	p.busy = true
	if action == "build" {
		m.storageBusy = true
	}
	p.ImageUpdates = ImageUpdates{Services: []ImageUpdate{}}
	m.wg.Add(1)
	go func() { defer m.wg.Done(); m.work(p, action, rev, content) }()
	return nil
}
func composeArgs(p Project, candidate string, cmd ...string) []string {
	args := []string{"compose", "--project-directory", p.Directory, "--project-name", p.ProjectName}
	for _, f := range p.EnvFiles {
		args = append(args, "--env-file", filepath.Join(p.Directory, f))
	}
	for i, f := range p.ComposeFiles {
		path := filepath.Join(p.Directory, f)
		if i == 0 && candidate != "" {
			path = candidate
		}
		args = append(args, "-f", path)
	}
	return append(args, cmd...)
}
func secrets(p Project) []string {
	var ss []string
	fs := append(append([]string{}, p.EnvFiles...), ".env")
	for _, f := range fs {
		path := filepath.Join(p.Directory, f)
		if e := rejectSymlinks(path); e != nil {
			continue
		}
		b, _ := os.ReadFile(path)
		for _, line := range strings.Split(string(b), "\n") {
			_, v, ok := strings.Cut(line, "=")
			v = strings.Trim(strings.TrimSpace(v), "\"'")
			if ok && len(v) >= 4 {
				ss = append(ss, v)
			}
		}
	}
	if secretFile := os.Getenv("QCU_PASSWORD_FILE"); secretFile != "" {
		if b, e := os.ReadFile(secretFile); e == nil {
			v := strings.TrimRight(string(b), "\r\n")
			if len(v) > 0 {
				ss = append(ss, v)
			}
		}
	}
	if v := os.Getenv("QCU_PASSWORD"); len(v) > 0 {
		ss = append(ss, v)
	}
	sort.Slice(ss, func(i, j int) bool { return len(ss[i]) > len(ss[j]) })
	return ss
}

type logWriter struct {
	mu        sync.Mutex
	m         *Manager
	p         *Managed
	pending   string
	secrets   []string
	truncated bool
}

func (w *logWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending += string(b)
	for {
		n := strings.IndexByte(w.pending, '\n')
		if n < 0 {
			break
		}
		w.line(w.pending[:n+1])
		w.pending = w.pending[n+1:]
	}
	if len(w.pending) > 65536 {
		w.pending = ""
		w.line("[oversized line hidden]\n")
		w.truncated = true
	}
	return len(b), nil
}
func (w *logWriter) line(s string) {
	if w.truncated {
		if strings.Contains(s, "\n") {
			w.truncated = false
		}
		return
	}
	for _, secret := range w.secrets {
		s = strings.ReplaceAll(s, secret, "[redacted]")
	}
	_ = w.m.update(w.p, func(o *Operation) {
		o.Log += s
		if len(o.Log) > 262144 {
			o.Log = "[earlier log output truncated]\n" + o.Log[len(o.Log)-240000:]
		}
	})
}
func (w *logWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.truncated && w.pending != "" {
		w.line(w.pending)
	}
	w.pending = ""
}
func (m *Manager) run(ctx context.Context, p *Managed, w *logWriter, candidate string, cmd ...string) error {
	phase := strings.Join(cmd, " ")
	if e := m.update(p, func(o *Operation) { o.Phase = phase }); e != nil {
		return e
	}
	fmt.Fprintln(w, "\n> docker compose "+phase)
	e := m.Exec.Run(ctx, p.Project, composeArgs(p.Project, candidate, cmd...), w)
	w.flush()
	if e != nil {
		return fmt.Errorf("%s : %w", phase, e)
	}
	p.mu.Lock()
	pe := p.persistError
	p.mu.Unlock()
	if pe != "" {
		return errors.New(pe)
	}
	return nil
}
func sameRevision(p Project, expected string) error {
	r, e := revision(p)
	if e != nil {
		return e
	}
	if r != expected {
		return errors.New("files changed outside the interface: operation stopped")
	}
	return nil
}
func (m *Manager) work(p *Managed, action, rev, content string) {
	if action == "build" {
		defer func() { m.mu.Lock(); m.storageBusy = false; m.mu.Unlock() }()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	w := &logWriter{m: m, p: p, secrets: secrets(p.Project)}
	var e error
	var oldImages []containerImage
	e = func() error {
		if e := sameRevision(p.Project, rev); e != nil {
			return e
		}
		if action == "apply" || action == "save" {
			original := filepath.Join(p.Project.Directory, p.Project.ComposeFiles[0])
			info, e := os.Stat(original)
			if e != nil {
				return e
			}
			f, e := os.CreateTemp(filepath.Dir(original), ".qcu-candidate-*.yaml")
			if e != nil {
				return e
			}
			candidate := f.Name()
			defer os.Remove(candidate)
			_, e = f.WriteString(content)
			ce := f.Close()
			if e == nil {
				e = ce
			}
			if e != nil {
				return e
			}
			if e = m.run(ctx, p, w, candidate, "config", "--quiet"); e != nil {
				return e
			}
			if e = sameRevision(p.Project, rev); e != nil {
				return e
			}
			previous, e := os.ReadFile(original)
			if e != nil {
				return e
			}
			backup := original + ".qcu-backup-" + p.Op.ID
			if e = atomicWrite(backup, previous, 0600); e != nil {
				return e
			}
			if e = atomicWrite(original, []byte(content), info.Mode()); e != nil {
				return e
			}
			fmt.Fprintln(w, "File saved; a backup of the previous version was created beside the Compose file.")
			rev, e = revision(p.Project)
			if e != nil {
				return e
			}
			if action == "save" {
				return nil
			}
		}
		if e = m.run(ctx, p, w, "", "config", "--quiet"); e != nil {
			return e
		}
		if action == "build" {
			return m.runBuild(ctx, p, w, rev)
		}
		if action == "apply" {
			if m.Images != nil {
				var captureErr error
				oldImages, captureErr = m.Images.containers(ctx, p.Project)
				if captureErr != nil {
					fmt.Fprintln(w, "Image cleanup will be skipped: cannot capture previous project images.")
					oldImages = nil
				}
			}
			if e = m.run(ctx, p, w, "", "pull"); e != nil {
				return e
			}
			if e = sameRevision(p.Project, rev); e != nil {
				return e
			}
			// Revalidate after pulling, before any downtime.
			if e = m.run(ctx, p, w, "", "config", "--quiet"); e != nil {
				return e
			}
			if e = sameRevision(p.Project, rev); e != nil {
				return e
			}
			if e = m.update(p, func(o *Operation) { o.NeedsStart = true }); e != nil {
				return e
			}
			if e = m.run(ctx, p, w, "", "down"); e != nil {
				return e
			}
			if e = m.update(p, func(o *Operation) { o.NeedsStart = true }); e != nil {
				return e
			}
		}
		if e = sameRevision(p.Project, rev); e != nil {
			return e
		}
		if action == "stop" {
			if e = m.update(p, func(o *Operation) { o.NeedsStart = true }); e != nil {
				return e
			}
			if e = m.run(ctx, p, w, "", "down"); e != nil {
				return e
			}
			return m.update(p, func(o *Operation) { o.NeedsStart = true })
		}
		if e = m.update(p, func(o *Operation) { o.NeedsStart = true }); e != nil {
			return e
		}
		if e = m.run(ctx, p, w, "", "up", "-d"); e != nil {
			return e
		}
		if e = m.update(p, func(o *Operation) { o.NeedsStart = false }); e != nil {
			return e
		}
		if action == "apply" && m.Images != nil {
			if e = sameRevision(p.Project, rev); e != nil {
				return e
			}
			if e = m.update(p, func(o *Operation) { o.Phase = "image cleanup" }); e != nil {
				return e
			}
			m.Images.cleanup(ctx, p.Project, oldImages, w)
		}
		return nil
	}()
	w.flush()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.busy = false
	p.Op.Running = false
	p.Op.Finished = time.Now()
	p.Op.Phase = "completed"
	p.Op.Result = "Operation completed."
	if e != nil {
		p.Op.Phase = "failed"
		p.Op.Result = e.Error()
		if p.Op.NeedsStart {
			p.Op.Result += " — The project may be stopped or partially running. Use Start / retry. There is no automatic rollback."
		}
	}
	for _, s := range w.secrets {
		p.Op.Result = strings.ReplaceAll(p.Op.Result, s, "[redacted]")
	}
	if e = m.persist(p); e != nil {
		p.persistError = "Operation log could not be persisted : " + e.Error()
	}
	// Keep bounded historical operation snapshots; no project data is removed.
	if e == nil {
		b, _ := json.MarshalIndent(p.Op, "", "  ")
		path := filepath.Join(m.StateDir, p.Project.ID+"-"+p.Op.ID+".history.json")
		if he := atomicWrite(path, b, 0600); he != nil {
			p.persistError = "Operation history could not be persisted : " + he.Error()
		}
		if action == "build" {
			if he := m.writeBuildSummary(p.Project.ID, p.Op); he != nil {
				p.persistError = "Build history could not be persisted: " + he.Error()
			}
		}
	}
}

func (m *Manager) get(id string) (*Managed, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.Projects[id]
	return p, ok
}
func (m *Manager) snapshot() []*Managed {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := make([]*Managed, 0, len(m.Order))
	for _, id := range m.Order {
		rows = append(rows, m.Projects[id])
	}
	return rows
}
