package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reef/internal/accounts"
	"reef/internal/docker"
	"reef/internal/history"
	"reef/internal/notifications"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func userServer(t *testing.T) (*Server, accounts.User, *http.Cookie, string) {
	t.Helper()
	s, _ := historyServer(t)
	dir := t.TempDir()
	var e error
	s.accounts, e = accounts.Open(dir, "long-password-123456")
	if e != nil {
		t.Fatal(e)
	}
	s.cfg.StateDir = dir
	s.discovery = &inventoryStub{rows: []docker.Container{}}
	admin := s.accounts.List()[0]
	cookie, csrf := userCookie(t, s, admin)
	return s, admin, cookie, csrf
}
func userCookie(t *testing.T, s *Server, u accounts.User) (*http.Cookie, string) {
	t.Helper()
	id, e := s.createUserSession(u)
	if e != nil {
		t.Fatal(e)
	}
	return &http.Cookie{Name: "qcu_session", Value: id}, s.sessions[sessionKey(id)].CSRF
}
func addReader(t *testing.T, s *Server, role string, hosts ...string) accounts.User {
	t.Helper()
	u, e := s.accounts.Change(accounts.User{Username: "test-" + role, Role: role, Hosts: hosts}, "long-password-123456", false)
	if e != nil {
		t.Fatal(e)
	}
	return u
}
func TestUserPermissionMatrixDeniesEveryMutationAndForeignHostBeforeDispatch(t *testing.T) {
	s, _, _, _ := userServer(t)
	token, _ := s.agents.Token("Assigned remote")
	remote, key, _ := s.agents.Enroll(token, 1)
	g, _ := s.agents.Begin(remote, key, "0.17.0", 1)
	s.agents.Control(remote, g, true)
	reader := addReader(t, s, "viewer", remote)
	cookie, csrf := userCookie(t, s, reader)
	h := s.Handler()
	reads := []string{"/api/containers", "/api/container-logs", "/api/state", "/api/document", "/api/stats", "/api/images", "/api/images/operation", "/api/volumes", "/api/networks", "/api/builds", "/api/builds/project", "/api/builds/history", "/api/builds/log", "/api/builds/cache", "/console", "/api/agent-update"}
	for _, path := range reads {
		w := call(h, "GET", path, "", "", cookie)
		if w.Code != 403 {
			t.Fatal("foreign Local access", path, w.Code, w.Body.String())
		}
	}
	mutations := []string{"/api/action", "/api/image-updates", "/api/projects/register", "/api/projects/unmanage", "/api/images/action", "/api/volumes/action", "/api/networks/action", "/api/builds/action", "/api/builds/cache/action", "/api/console/open", "/api/console/input", "/api/console/resize", "/api/console/close", "/api/console/stream", "/api/agent-update", "/api/hosts", "/api/webhook", "/api/users", "/api/monitoring"}
	for _, path := range mutations {
		w := call(h, "POST", path+"?host="+remote, "csrf="+csrf+"&confirmed=yes", "http://reef.test", cookie)
		if w.Code != 403 {
			t.Fatal("viewer mutated", path, w.Code, w.Body.String())
		}
	}
	w := call(h, "GET", "/api/hosts", "", "", cookie)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"local_allowed":true`) || !strings.Contains(w.Body.String(), remote) {
		t.Fatal(w.Body.String())
	}
	limited := addReader(t, s, "admin", "local")
	adminCookie, adminCSRF := userCookie(t, s, limited)
	for _, path := range mutations {
		if path == "/api/console/stream" {
			continue
		}
		w = call(h, "POST", path+"?host="+remote, "csrf="+adminCSRF+"&confirmed=yes", "http://reef.test", adminCookie)
		if w.Code != 403 {
			t.Fatal("limited admin foreign/global access", path, w.Code)
		}
	}
	if w = call(h, "GET", "/api/containers", "", "", adminCookie); w.Code != 200 {
		t.Fatal("authorized read denied", w.Code)
	}
}
func TestHistoryHostFilteringPrecedesPaginationAndCursorsAreUserBound(t *testing.T) {
	s, _, adminCookie, _ := userServer(t)
	remote := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	for id, name := range map[string]string{remote: "allowed", other: "hidden"} {
		e := notifications.New("project", "apply", "completed", "", name+"-secret")
		if e := s.history.Accept(id, name, []notifications.Event{e}); e != nil {
			t.Fatal(e)
		}
	}
	s.recordEvent(notifications.New("project", "apply", "completed", "", "local-secret"))
	drainHistory(s)
	reader := addReader(t, s, "viewer", remote)
	cookie, _ := userCookie(t, s, reader)
	w := call(s.Handler(), "GET", "/api/history?host=all", "", "", cookie)
	var page history.Page
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || w.Code != 200 || page.Total != 1 || page.RetainedEvents != 1 || strings.Contains(w.Body.String(), "hidden") || strings.Contains(w.Body.String(), "local-secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	if page.Cursor == "" || strings.Trim(page.Cursor, "0123456789") == "" {
		t.Fatal("global sequence exposed", page.Cursor)
	}
	w = call(s.Handler(), "GET", "/api/history?host=all&cursor="+url.QueryEscape(page.Cursor), "", "", adminCookie)
	if w.Code != 400 {
		t.Fatal("cross-user cursor accepted", w.Code)
	}
	w = call(s.Handler(), "GET", "/api/history?host="+other, "", "", cookie)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestUserChangesRevokePersistedSessionsAndActiveConsoles(t *testing.T) {
	s, _, adminCookie, adminCSRF := userServer(t)
	reader := addReader(t, s, "viewer", "local")
	cookie, userCSRF := userCookie(t, s, reader)
	closed := false
	s.consoles = map[string]*consoleSession{"test": {owner: userCSRF, cancel: func() { closed = true }}}
	form := url.Values{"csrf": {adminCSRF}, "confirmed": {"yes"}, "action": {"update"}, "id": {reader.ID}, "revision": {strconv.FormatUint(reader.Revision, 10)}, "role": {"viewer"}, "hosts": {"local"}, "disabled": {"true"}}
	w := call(s.Handler(), "POST", "/api/users", form.Encode(), "http://reef.test", adminCookie)
	if w.Code != 200 || !closed {
		t.Fatal(w.Code, w.Body.String(), closed)
	}
	if w = call(s.Handler(), "GET", "/api/containers", "", "", cookie); w.Code != 401 {
		t.Fatal("revoked cookie accepted", w.Code)
	}
	restarted := newServer(s.m, s.cfg, "bootstrap-can-change-now")
	var e error
	restarted.accounts, e = accounts.Open(s.cfg.StateDir, "bootstrap-can-change-now")
	if e != nil {
		t.Fatal(e)
	}
	if e = restarted.restoreSessions(); e != nil {
		t.Fatal(e)
	}
	if w = call(restarted.Handler(), "GET", "/api/containers", "", "", cookie); w.Code != 401 {
		t.Fatal("revoked persisted session accepted", w.Code)
	}
}
func TestDeferredResponseAndRelayCannotOutliveUserRevocation(t *testing.T) {
	s, _, _, _ := userServer(t)
	u := addReader(t, s, "viewer", "local")
	cookie, _ := userCookie(t, s, u)
	r := httptest.NewRequest("GET", "http://reef.test/api/state", nil)
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	guard := &permissionWriter{ResponseWriter: rec, allowed: func() bool { _, ok := s.getSession(r); return ok }}
	u.Disabled = true
	s.accounts.Change(u, "", false)
	if n, e := guard.Write([]byte("private-delayed-data")); n != 0 || e != io.ErrClosedPipe || strings.Contains(rec.Body.String(), "private") {
		t.Fatal(n, e, rec.Body.String())
	}
}
func TestMigrationRejectsOldSharedCookiesAndLoginNeverEchoesPasswords(t *testing.T) {
	dir := t.TempDir()
	s := persistentServer(t, dir, "long-password-123456")
	cookie := loginSession(t, s)
	var e error
	s.accounts, e = accounts.Open(dir, "long-password-123456")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.restoreSessions(); e != nil {
		t.Fatal(e)
	}
	if w := call(s.Handler(), "GET", "/api/state", "", "", cookie); w.Code != 401 {
		t.Fatal("shared cookie survived migration")
	}
	w := call(s.Handler(), "POST", "/login", "username=admin&password=wrong-password-value", "http://reef.test", nil)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "Incorrect username or password") || strings.Contains(w.Body.String(), "wrong-password-value") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(s.Handler(), "POST", "/login", "username=admin&password=long-password-123456", "http://reef.test", nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "auth", "users.json"))
	if strings.Contains(string(raw), "long-password-123456") {
		t.Fatal("password stored in plaintext")
	}
}

type delayedUserInventory struct {
	begun, release chan struct{}
	calls          atomic.Int32
}

func (d *delayedUserInventory) List(ctx context.Context) ([]docker.Container, error) {
	if d.calls.Add(1) == 1 {
		return []docker.Container{}, nil
	}
	close(d.begun)
	select {
	case <-d.release:
		return []docker.Container{{ID: strings.Repeat("a", 64), Name: "private-remote-response", State: "running"}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestRemoteReadIsAllowedButDelayedResultIsWithheldAfterRevocation(t *testing.T) {
	s, _, _, _ := userServer(t)
	m, _, _ := fixture(t, &fakeExecutor{})
	backend := newServer(m, Config{}, "test-password-long")
	delayed := &delayedUserInventory{begun: make(chan struct{}), release: make(chan struct{})}
	backend.discovery = delayed
	host, stop := relayAgentFixture(t, s, backend, "Remote")
	defer stop()
	reader := addReader(t, s, "viewer", host)
	cookie, _ := userCookie(t, s, reader)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(s.Handler(), "GET", "/api/containers?host="+host, "", "", cookie) }()
	select {
	case <-delayed.begun:
	case <-time.After(3 * time.Second):
		t.Fatal("authorized viewer read not relayed")
	}
	reader.Disabled = true
	if _, e := s.accounts.Change(reader, "", false); e != nil {
		t.Fatal(e)
	}
	close(delayed.release)
	select {
	case w := <-done:
		if strings.Contains(w.Body.String(), "private-remote-response") {
			t.Fatal("revoked delayed response leaked", w.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("response did not terminate")
	}
}

func TestRestrictedLocalHistoryExcludesGlobalAgentAdministration(t *testing.T) {
	s, _, _, _ := userServer(t)
	s.recordEvent(notifications.New("agent", "enroll", "completed", "", "foreign-host-name"))
	s.recordEvent(notifications.New("project", "start", "completed", "", "local-project"))
	drainHistory(s)
	u := addReader(t, s, "viewer", "local")
	cookie, _ := userCookie(t, s, u)
	w := call(s.Handler(), "GET", "/api/history?host=all", "", "", cookie)
	var p history.Page
	json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 200 || p.Total != 1 || p.RetainedEvents != 1 || strings.Contains(w.Body.String(), "foreign-host-name") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAccountsOnRevokedHostsCanStillBeRevokedAndDeleted(t *testing.T) {
	s, _, cookie, csrf := userServer(t)
	token, _ := s.agents.Token("retired")
	host, _, _ := s.agents.Enroll(token, 1)
	u := addReader(t, s, "viewer", host)
	s.agents.Change(host, "", true)
	for _, action := range []string{"revoke", "delete"} {
		form := url.Values{"csrf": {csrf}, "confirmed": {"yes"}, "action": {action}, "id": {u.ID}, "revision": {strconv.FormatUint(u.Revision, 10)}}
		w := call(s.Handler(), "POST", "/api/users", form.Encode(), "http://reef.test", cookie)
		if w.Code != 200 {
			t.Fatal(action, w.Code, w.Body.String())
		}
		u, _ = s.accounts.User(u.ID)
	}
}
