package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reef/internal/docker"
)

type inventoryStub struct {
	rows    []docker.Container
	failure error
	calls   int
}

func (f *inventoryStub) List(context.Context) ([]docker.Container, error) {
	f.calls++
	return f.rows, f.failure
}
func TestInventoryRequiresAuthAndDoesNotAdoptProjects(t *testing.T) {
	executor := &fakeExecutor{}
	m, p, _ := fixture(t, executor)
	s := newServer(m, Config{}, "long-password-123456")
	reader := &inventoryStub{rows: []docker.Container{{ID: "running", Name: "Real application", State: "running", ComposeProject: "unconfigured"}, {ID: "stopped", Name: "Stopped application", State: "exited", ComposeProject: p.ProjectName}}}
	s.discovery = reader
	h := s.Handler()
	if w := call(h, "GET", "/api/containers", "", "", nil); w.Code != 401 || reader.calls != 0 {
		t.Fatal(w.Code, reader.calls)
	}
	s.sessions[sessionKey("inventory-test")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "inventory-test"}
	w := call(h, "GET", "/api/containers", "", "", cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Containers []struct {
			State   string `json:"state"`
			Managed string `json:"managed_project_id"`
		}
	}
	if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil {
		t.Fatal(e)
	}
	if len(response.Containers) != 2 || response.Containers[0].Managed != "" || response.Containers[1].Managed != p.ID || response.Containers[1].State != "exited" {
		t.Fatal(response)
	}
	if len(m.Projects) != 1 {
		t.Fatal("discovery authorized another project")
	}
	w = call(h, "GET", "/api/document?id=unconfigured", "", "", cookie)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = call(h, "POST", "/api/containers", "csrf=csrf", "http://reef.test", cookie)
	if w.Code != 405 || reader.calls != 1 {
		t.Fatal(w.Code, reader.calls)
	}
	w = call(h, "POST", "/api/action", "csrf=csrf&id=unconfigured&action=stop", "http://reef.test", cookie)
	if w.Code != 409 || len(executor.calls) != 0 {
		t.Fatal("Discovery granted an operation", w.Code, executor.calls)
	}
	reader.failure = errors.New("Docker unavailable")
	w = call(h, "GET", "/api/containers", "", "", cookie)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Docker unavailable") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestNoManagedProjectsIsValidForInventoryOnly(t *testing.T) {
	// A fresh installation must not register a sample project to see real Docker.
	c := Config{StateDir: t.TempDir()}
	path := filepath.Join(t.TempDir(), "config.json")
	b, _ := json.Marshal(c)
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	loaded, e := loadConfig(path)
	if e != nil {
		t.Fatal(e)
	}
	m, e := newManager(loaded, &fakeExecutor{})
	if e != nil || len(m.Projects) != 0 {
		t.Fatal(e)
	}
}
