package app

import (
	"net/url"
	"os"
	"path/filepath"
	"reef/internal/agents"
	"reef/internal/notifications"
	"reflect"
	"strings"
	"testing"
)

func TestProjectRestartOnlyRestartsSavedExistingProject(t *testing.T) {
	f := &fakeExecutor{}
	m, p, rev := fixture(t, f)
	before, _ := os.ReadFile(filepath.Join(p.Directory, p.ComposeFiles[0]))
	events := []notifications.Event{}
	m.Notify = func(e notifications.Event) { events = append(events, e) }
	launch(t, m, p, rev, "restart")
	if !reflect.DeepEqual(f.calls, []string{"config --quiet", "restart"}) || m.Projects[p.ID].Op.Phase != "completed" || m.Projects[p.ID].Op.NeedsStart {
		t.Fatal(f.calls, m.Projects[p.ID].Op)
	}
	after, _ := os.ReadFile(filepath.Join(p.Directory, p.ComposeFiles[0]))
	if string(after) != string(before) {
		t.Fatal("Restart saved editor content")
	}
	assertFiles(t, p)
	for _, args := range f.args {
		if !reflect.DeepEqual(args[:5], []string{"compose", "--project-directory", p.Directory, "--project-name", p.ProjectName}) {
			t.Fatal(args)
		}
	}
	if len(events) != 2 || events[0].Action != "restart" || events[1].Status != "completed" || !events[1].Valid() {
		t.Fatal(events)
	}
	backups, _ := filepath.Glob(filepath.Join(p.Directory, "*.qcu-backup-*"))
	if len(backups) != 0 {
		t.Fatal("Restart created backups")
	}
}
func TestProjectRestartFailureExternalChangeAndRecoveryBlock(t *testing.T) {
	for _, failure := range []string{"config --quiet", "restart"} {
		f := &fakeExecutor{fail: failure}
		m, p, rev := fixture(t, f)
		launch(t, m, p, rev, "restart")
		op := m.Projects[p.ID].Op
		if op.Phase != "failed" || op.NeedsStart != (failure == "restart") {
			t.Fatal(op)
		}
		if failure == "config --quiet" && len(f.calls) != 1 {
			t.Fatal(f.calls)
		}
	}
	f := &fakeExecutor{}
	m, p, rev := fixture(t, f)
	m.Projects[p.ID].Op.NeedsStart = true
	if e := m.begin(p.ID, "restart", rev, ""); e == nil {
		t.Fatal("Restart accepted for a project needing Start")
	}
	m.Projects[p.ID].Op.NeedsStart = false
	os.WriteFile(filepath.Join(p.Directory, "custom.env"), []byte("CHANGED=yes"), 0600)
	if e := m.begin(p.ID, "restart", rev, ""); e == nil || len(f.calls) != 0 {
		t.Fatal("Outdated restart reached Docker", e)
	}
	f = &fakeExecutor{gate: make(chan struct{}), entered: make(chan struct{})}
	m, p, rev = fixture(t, f)
	if e := m.begin(p.ID, "restart", rev, ""); e != nil {
		t.Fatal(e)
	}
	<-f.entered
	os.WriteFile(filepath.Join(p.Directory, "custom.env"), []byte("CHANGED=yes"), 0600)
	close(f.gate)
	m.wg.Wait()
	if !reflect.DeepEqual(f.calls, []string{"config --quiet"}) {
		t.Fatal("Changed configuration reached restart", f.calls)
	}
}
func TestProjectRestartAPIConfirmationPermissionsAndRemoteCapability(t *testing.T) {
	central, _, cookie, csrf := userServer(t)
	p := central.m.Projects["test"].Project
	rev, _ := revision(p)
	form := url.Values{"id": {p.ID}, "action": {"restart"}, "revision": {rev}, "csrf": {csrf}}
	for _, test := range []struct {
		confirmed, token string
		status           int
	}{{"", csrf, 400}, {"yes", "wrong", 403}} {
		form.Set("confirmed", test.confirmed)
		form.Set("csrf", test.token)
		w := call(central.Handler(), "POST", "/api/action", form.Encode(), "http://reef.test", cookie)
		if w.Code != test.status {
			t.Fatal(w.Code, w.Body)
		}
	}
	reader := addReader(t, central, "viewer", "local")
	readerCookie, readerCSRF := userCookie(t, central, reader)
	form.Set("csrf", readerCSRF)
	w := call(central.Handler(), "POST", "/api/action", form.Encode(), "http://reef.test", readerCookie)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	backend, _ := historyServer(t)
	remoteProject := backend.m.Projects["test"].Project
	remoteRev, _ := revision(remoteProject)
	host, stop := relayAgentFixture(t, central, backend, "restart-agent")
	defer stop()
	form.Set("revision", remoteRev)
	form.Set("csrf", csrf)
	w = call(central.Handler(), "POST", "/api/action?host="+host, form.Encode(), "http://reef.test", cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "0.28.0") {
		t.Fatal(w.Code, w.Body)
	}
	f := backend.m.Exec.(*fakeExecutor)
	f.mu.Lock()
	calls := len(f.calls)
	f.mu.Unlock()
	if calls != 0 {
		t.Fatal("Old agent command dispatched")
	}
	// A separately negotiated capable host executes only its own saved project.
	host2, stop2 := relayAgentFixture(t, central, backend, "capable-restart-agent", agents.ProjectRestartCapability)
	defer stop2()
	w = call(central.Handler(), "POST", "/api/action?host="+host2, form.Encode(), "http://reef.test", cookie)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	backend.m.wg.Wait()
	if !reflect.DeepEqual(f.calls, []string{"config --quiet", "restart"}) {
		t.Fatal(f.calls)
	}
	local := central.m.Exec.(*fakeExecutor)
	local.mu.Lock()
	calls = len(local.calls)
	local.mu.Unlock()
	if calls != 0 {
		t.Fatal("Remote restart reached Local")
	}
	form.Set("revision", rev)
	w = call(central.Handler(), "POST", "/api/action", form.Encode(), "http://reef.test", cookie)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	central.m.wg.Wait()
	if !reflect.DeepEqual(local.calls, []string{"config --quiet", "restart"}) {
		t.Fatal(local.calls)
	}
}
