package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeExecutor struct {
	mu      sync.Mutex
	calls   []string
	args    [][]string
	fail    string
	gate    chan struct{}
	entered chan struct{}
	check   func(Project, []string) error
	output  string
}

func (f *fakeExecutor) Run(ctx context.Context, p Project, args []string, w io.Writer) error {
	cmd := ""
	for i, a := range args {
		if a == "config" || a == "pull" || a == "down" || a == "up" {
			cmd = strings.Join(args[i:], " ")
			break
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, cmd)
	f.args = append(f.args, append([]string{}, args...))
	n := len(f.calls)
	f.mu.Unlock()
	if f.check != nil {
		if e := f.check(p, args); e != nil {
			return e
		}
	}
	if n == 1 && f.entered != nil {
		close(f.entered)
		select {
		case <-f.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.output != "" { // Exercise secret redaction across split writes.
		mid := len(f.output) / 2
		io.WriteString(w, f.output[:mid])
		io.WriteString(w, f.output[mid:])
	}
	if f.fail == cmd {
		return errors.New("échec injecté")
	}
	return nil
}
func fixture(t *testing.T, f *fakeExecutor) (*Manager, Project, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := Project{ID: "test", Name: "Test", Directory: root, ComposeFiles: []string{"compose.yaml", "override.yaml"}, ProjectName: "existing-prod", EnvFiles: []string{"custom.env"}}
	for name, content := range map[string]string{"compose.yaml": "services:\n  app:\n    image: old\n", "override.yaml": "services: {}\n", "custom.env": "KEY=secret-custom\n", ".env": "PASS=secret-value\n", "data/keep.txt": "precious", "config/keep.txt": "unchanged"} {
		path := filepath.Join(root, name)
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(content), 0640); e != nil {
			t.Fatal(e)
		}
	}
	m, e := newManager(Config{StateDir: t.TempDir(), Projects: []Project{p}}, f)
	if e != nil {
		t.Fatal(e)
	}
	rev, e := revision(p)
	if e != nil {
		t.Fatal(e)
	}
	return m, p, rev
}
func launch(t *testing.T, m *Manager, p Project, rev, action string) {
	t.Helper()
	if e := m.begin(p.ID, action, rev, "services:\n  app:\n    image: new\n"); e != nil {
		t.Fatal(e)
	}
	m.wg.Wait()
}
func assertFiles(t *testing.T, p Project) {
	t.Helper()
	for f, want := range map[string]string{"data/keep.txt": "precious", "config/keep.txt": "unchanged", "override.yaml": "services: {}\n", "custom.env": "KEY=secret-custom\n", ".env": "PASS=secret-value\n"} {
		b, e := os.ReadFile(filepath.Join(p.Directory, f))
		if e != nil || string(b) != want {
			t.Fatalf("fichier altéré %s : %q %v", f, b, e)
		}
	}
}
func TestApplyOrderPathsBackupPreservation(t *testing.T) {
	f := &fakeExecutor{}
	m, p, rev := fixture(t, f)
	f.check = func(p Project, args []string) error {
		for i, a := range args {
			if a == "-f" && strings.Contains(args[i+1], ".qcu-candidate-") {
				if filepath.Dir(args[i+1]) != p.Directory {
					t.Error("candidat hors dossier original")
				}
			}
		}
		if !reflect.DeepEqual(args[:5], []string{"compose", "--project-directory", p.Directory, "--project-name", "existing-prod"}) {
			t.Error(args)
		}
		if !strings.Contains(strings.Join(args, " "), "--env-file "+filepath.Join(p.Directory, "custom.env")) {
			t.Error("env_file perdu")
		}
		return nil
	}
	launch(t, m, p, rev, "apply")
	if !reflect.DeepEqual(f.calls, []string{"config --quiet", "config --quiet", "pull", "config --quiet", "down", "up -d"}) {
		t.Fatal(f.calls)
	}
	assertFiles(t, p)
	backups, _ := filepath.Glob(filepath.Join(p.Directory, "compose.yaml.qcu-backup-*"))
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	b, _ := os.ReadFile(backups[0])
	if !strings.Contains(string(b), "image: old") {
		t.Fatal("sauvegarde incorrecte")
	}
	info, _ := os.Stat(filepath.Join(p.Directory, "compose.yaml"))
	if info.Mode().Perm() != 0640 {
		t.Fatal("permissions perdues")
	}
	b, _ = os.ReadFile(filepath.Join(p.Directory, "compose.yaml"))
	if !strings.Contains(string(b), "image: new") {
		t.Fatal("non enregistré")
	}
	if m.Projects[p.ID].Op.NeedsStart || m.Projects[p.ID].Op.Phase != "completed" {
		t.Fatal(m.Projects[p.ID].Op)
	}
}
func TestFailureBeforeDown(t *testing.T) {
	for _, fail := range []string{"config --quiet", "pull"} {
		t.Run(fail, func(t *testing.T) {
			f := &fakeExecutor{fail: fail}
			m, p, rev := fixture(t, f)
			launch(t, m, p, rev, "apply")
			for _, c := range f.calls {
				if c == "down" || c == "up -d" {
					t.Fatal(f.calls)
				}
			}
			if m.Projects[p.ID].Op.Phase != "failed" {
				t.Fatal("échec absent")
			}
			assertFiles(t, p)
			if fail == "config --quiet" {
				b, _ := os.ReadFile(filepath.Join(p.Directory, "compose.yaml"))
				if strings.Contains(string(b), "image: new") {
					t.Fatal("enregistrement avant validation")
				}
			}
		})
	}
}
func TestUpFailureAndRetry(t *testing.T) {
	f := &fakeExecutor{fail: "up -d"}
	m, p, rev := fixture(t, f)
	launch(t, m, p, rev, "apply")
	if !m.Projects[p.ID].Op.NeedsStart || !strings.Contains(m.Projects[p.ID].Op.Result, "rollback") {
		t.Fatal(m.Projects[p.ID].Op)
	}
	f.fail = ""
	f.calls = nil
	rev, _ = revision(p)
	launch(t, m, p, rev, "start")
	if !reflect.DeepEqual(f.calls, []string{"config --quiet", "up -d"}) || m.Projects[p.ID].Op.NeedsStart {
		t.Fatal(f.calls)
	}
	assertFiles(t, p)
}
func TestLockObsoleteAndExternalChange(t *testing.T) {
	f := &fakeExecutor{gate: make(chan struct{}), entered: make(chan struct{})}
	m, p, rev := fixture(t, f)
	if e := m.begin(p.ID, "apply", rev, "services: {}\n"); e != nil {
		t.Fatal(e)
	}
	<-f.entered
	for _, action := range []string{"save", "start", "stop", "apply"} {
		if e := m.begin(p.ID, action, rev, "services: {}\n"); e == nil {
			t.Fatal("concurrence acceptée")
		}
	}
	if e := os.WriteFile(filepath.Join(p.Directory, ".env"), []byte("CHANGED=yes"), 0600); e != nil {
		t.Fatal(e)
	}
	close(f.gate)
	m.wg.Wait()
	if !reflect.DeepEqual(f.calls, []string{"config --quiet"}) {
		t.Fatal(f.calls)
	}
	if e := m.begin(p.ID, "save", rev, "services: {}\n"); e == nil {
		t.Fatal("édition obsolète acceptée")
	}
}
func TestSaveOnlyAndLogRedaction(t *testing.T) {
	f := &fakeExecutor{output: "<script>alert('secret-value secret-custom')</script>\n"}
	m, p, rev := fixture(t, f)
	launch(t, m, p, rev, "save")
	if !reflect.DeepEqual(f.calls, []string{"config --quiet"}) {
		t.Fatal(f.calls)
	}
	if strings.Contains(m.Projects[p.ID].Op.Log, "secret-value") || strings.Contains(m.Projects[p.ID].Op.Log, "secret-custom") {
		t.Fatal("secret divulgué")
	}
	assertFiles(t, p)
}
func TestRestartMarksUnknown(t *testing.T) {
	f := &fakeExecutor{}
	m, p, _ := fixture(t, f)
	mp := m.Projects[p.ID]
	mp.Op = Operation{ID: "old", Running: true, Phase: "down"}
	if e := m.persist(mp); e != nil {
		t.Fatal(e)
	}
	reloaded, e := newManager(Config{StateDir: m.StateDir, Projects: []Project{p}}, f)
	if e != nil {
		t.Fatal(e)
	}
	op := reloaded.Projects[p.ID].Op
	if op.Running || op.Phase != "interrupted" || !op.NeedsStart {
		t.Fatal(op)
	}
}
func TestPullExternalChangeAbortsBeforeDown(t *testing.T) {
	f := &fakeExecutor{}
	m, p, rev := fixture(t, f)
	f.check = func(p Project, args []string) error {
		if args[len(args)-1] == "pull" {
			return os.WriteFile(filepath.Join(p.Directory, "override.yaml"), []byte("services: {changed: {}}"), 0600)
		}
		return nil
	}
	launch(t, m, p, rev, "apply")
	for _, c := range f.calls {
		if c == "down" {
			t.Fatal("arrêt malgré changement externe")
		}
	}
}
func TestStopNoVolumeFlag(t *testing.T) {
	f := &fakeExecutor{}
	m, p, rev := fixture(t, f)
	launch(t, m, p, rev, "stop")
	if !reflect.DeepEqual(f.calls, []string{"config --quiet", "down"}) {
		t.Fatal(f.calls)
	}
	for _, args := range f.args {
		for _, a := range args {
			if a == "-v" || a == "prune" {
				t.Fatal("commande interdite")
			}
		}
	}
	assertFiles(t, p)
}

func TestRequestDisconnectDoesNotCancelOperation(t *testing.T) {
	f := &fakeExecutor{gate: make(chan struct{}), entered: make(chan struct{})}
	m, p, rev := fixture(t, f)
	s := newServer(m, Config{}, "long-password-123456")
	s.sessions["test-session"] = session{CSRF: "csrf-test", Expires: time.Now().Add(time.Hour)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := url.Values{"id": {p.ID}, "action": {"apply"}, "revision": {rev}, "content": {"services: {}\n"}, "csrf": {"csrf-test"}}.Encode()
	r := httptest.NewRequest("POST", "http://reef.test/api/action", strings.NewReader(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://reef.test")
	r.AddCookie(&http.Cookie{Name: "qcu_session", Value: "test-session"})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	<-f.entered
	cancel()
	close(f.gate)
	m.wg.Wait()
	if m.Projects[p.ID].Op.Phase != "completed" {
		t.Fatal("fermeture du client a interrompu le worker", m.Projects[p.ID].Op)
	}
	if f.calls[len(f.calls)-1] != "up -d" {
		t.Fatal(f.calls)
	}
}
