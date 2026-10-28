package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reef/internal/agents"
	"reef/internal/agentupdate"
	"reef/internal/buildinfo"
	"reef/internal/docker"
	"strings"
	"testing"
	"time"
)

func updateServer(t *testing.T) *Server {
	t.Helper()
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{}, "unused-password")
	var err error
	s.agentUpdates, err = agentupdate.Open(filepath.Join(t.TempDir(), "updates"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.agentUpdates.Write("heartbeat.json", agentupdate.Heartbeat{At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return s
}
func setUpdateBuild(t *testing.T, version string) {
	t.Helper()
	oldV, oldR := buildinfo.Version, buildinfo.Revision
	buildinfo.Version, buildinfo.Revision = version, strings.Repeat("b", 40)
	t.Cleanup(func() { buildinfo.Version, buildinfo.Revision = oldV, oldR })
}
func TestAgentUpdateAdmissionAndMaintenanceLock(t *testing.T) {
	setUpdateBuild(t, "0.10.0")
	s := updateServer(t)
	s.consoles = map[string]*consoleSession{"open": {}}
	if err := s.beginAgentUpdate("0.11.0", buildinfo.Revision); err == nil {
		t.Fatal("Update admitted during a console session")
	}
	s.consoles = nil
	p := s.m.Projects["test"]
	p.busy = true
	if err := s.beginAgentUpdate("0.11.0", buildinfo.Revision); err == nil {
		t.Fatal("Update admitted during a project operation")
	}
	p.busy = false
	p.ImageUpdates.Checking = true
	if err := s.beginAgentUpdate("0.11.0", buildinfo.Revision); err == nil {
		t.Fatal("Update admitted during image checks")
	}
	p.ImageUpdates.Checking = false
	if err := s.beginAgentUpdate("0.11.0", buildinfo.Revision); err != nil {
		t.Fatal(err)
	}
	if !s.m.storageBusy || !s.m.agentUpdating || !s.agentUpdating {
		t.Fatal("Maintenance lock missing")
	}
	if err := s.beginAgentUpdate("0.11.0", buildinfo.Revision); err == nil {
		t.Fatal("Duplicate update admitted")
	}
	if err := s.m.checkImages("test", false); err == nil {
		t.Fatal("Image check admitted during maintenance")
	}
	if err := s.m.begin("test", "stop", "", ""); err == nil {
		t.Fatal("Compose operation admitted during maintenance")
	}
	o, err := s.agentUpdates.Operation()
	if err != nil {
		t.Fatal(err)
	}
	o.Phase = "failed"
	s.agentUpdates.Write("operation.json", o)
	if err = s.syncAgentUpdate(); err != nil {
		t.Fatal(err)
	}
	if s.m.storageBusy || s.m.agentUpdating || s.agentUpdating {
		t.Fatal("Completed maintenance lock retained")
	}
}
func TestAgentUpdateUnavailableOrInvalidTargetsDoNotQueue(t *testing.T) {
	setUpdateBuild(t, "0.10.0")
	s := updateServer(t)
	for _, v := range []string{"0.9.0", "0.10.0", "latest", "0.11.0;id"} {
		if err := s.beginAgentUpdate(v, buildinfo.Revision); err == nil {
			t.Fatal(v)
		}
	}
	s.agentUpdates.Write("heartbeat.json", agentupdate.Heartbeat{At: time.Now().Add(-time.Minute)})
	if err := s.beginAgentUpdate("0.11.0", buildinfo.Revision); err == nil {
		t.Fatal("Offline updater accepted")
	}
	o, err := s.agentUpdates.Operation()
	if err != nil || o.ID != "" {
		t.Fatal(o, err)
	}
}
func TestAgentUpdateRestoresMaintenanceFromDurableState(t *testing.T) {
	s := updateServer(t)
	o := agentupdate.Operation{ID: strings.Repeat("a", 48), Phase: "reconnecting"}
	s.agentUpdates.Write("operation.json", o)
	if err := s.syncAgentUpdate(); err != nil {
		t.Fatal(err)
	}
	if !s.m.agentUpdating || !s.m.storageBusy {
		t.Fatal("Restart lost maintenance state")
	}
}
func TestRemoteAgentUpdatesPinCentralReleaseAndEnforceAuthentication(t *testing.T) {
	setUpdateBuild(t, "0.10.0")
	local, _, _ := fixture(t, &fakeExecutor{})
	central := newServer(local, Config{AgentsEnabled: true}, "long-password-123456")
	central.agents, _ = agents.Open(t.TempDir())
	central.sessions[sessionKey("browser")] = session{CSRF: "browser-csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "browser"}
	remote := updateServer(t)
	remote.discovery = &logStub{inventoryStub: inventoryStub{rows: []docker.Container{}}}
	host, _ := relayAgentFixture(t, central, remote, "remote", agents.UpdatesCapability)
	legacy, _ := relayAgentFixture(t, central, remote, "legacy")
	h := central.Handler()
	path := "/api/agent-update?host=" + host
	form := url.Values{"csrf": {"browser-csrf"}, "confirmed": {"yes"}, "version": {"0.10.0"}, "revision": {strings.Repeat("f", 40)}, "image": {"evil/image:latest"}}.Encode()
	for _, tc := range []struct {
		body, origin string
		cookie       *http.Cookie
		code         int
	}{
		{form, "http://reef.test", nil, 401},
		{form, "http://evil.test", cookie, 403},
		{strings.Replace(form, "browser-csrf", "wrong", 1), "http://reef.test", cookie, 403},
		{strings.Replace(form, "confirmed=yes", "confirmed=no", 1), "http://reef.test", cookie, 409},
		{strings.Replace(form, "0.10.0", "0.12.0", 1), "http://reef.test", cookie, 409},
	} {
		w := call(h, "POST", path, tc.body, tc.origin, tc.cookie)
		if w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String(), tc.code)
		}
	}
	if w := call(h, "POST", "/api/agent-update?host="+legacy, form, "http://reef.test", cookie); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := call(h, "GET", path, "", "", cookie); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// The test binaries share buildinfo. Test backend admission separately;
	// TestCentralUpdateSubstitutesReleaseMetadata verifies central dispatch.
	request := agents.Command{Method: "POST", Path: "/api/agent-update", Owner: strings.Repeat("a", 64), Expires: time.Now().Add(time.Hour), Form: url.Values{"version": {"0.11.0"}, "revision": {buildinfo.Revision}, "confirmed": {"yes"}}}
	w := newRelayRecorder(t, remote, request)
	if w != 202 {
		t.Fatal(w)
	}
	o, _ := remote.agentUpdates.Operation()
	if o.Version != "0.11.0" || o.Revision != buildinfo.Revision {
		t.Fatal(o)
	}
	if central.m.storageBusy {
		t.Fatal("Central host entered remote maintenance")
	}
	central.agents.Change(host, "", true)
	if w := call(h, "GET", path, "", "", cookie); w.Code != 409 {
		t.Fatal("Revoked host accepted", w.Code)
	}
}
func newRelayRecorder(t *testing.T, s *Server, command agents.Command) int {
	t.Helper()
	// Exercise AgentBackend's owner/session validation, not a browser session.
	w := httptest.NewRecorder()
	(&AgentBackend{server: s, handler: s.Handler()}).Serve(context.Background(), w, command)
	return w.Code
}
func TestCentralUpdateSubstitutesReleaseMetadata(t *testing.T) {
	setUpdateBuild(t, "0.11.0")
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{AgentsEnabled: true}, "long-password-123456")
	s.agents, _ = agents.Open("")
	s.sessions[sessionKey("browser")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	token, _ := s.agents.Token("remote")
	id, key, _ := s.agents.Enroll(token, agents.Protocol)
	gen, _ := s.agents.Begin(id, key, "0.10.0", agents.Protocol)
	s.agents.Control(id, gen, true)
	s.agents.Updates(id, gen, true)
	s.relay.Connect(id, gen)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan agents.Command, 1)
	go func() {
		batch, err := s.relay.Poll(ctx, id, gen)
		if err != nil || len(batch.Commands) != 1 {
			return
		}
		cmd := batch.Commands[0]
		done <- cmd
		s.relay.Results(id, gen, []agents.Frame{{ID: cmd.ID, Sequence: 1, Status: 202}, {ID: cmd.ID, Sequence: 2, Done: true}})
	}()
	w := call(s.Handler(), "POST", "/api/agent-update?host="+id, "csrf=csrf&confirmed=yes&version=0.11.0&revision=evil&image=evil", "http://reef.test", &http.Cookie{Name: "qcu_session", Value: "browser"})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	select {
	case cmd := <-done:
		if cmd.Form.Get("revision") != buildinfo.Revision || cmd.Form.Get("version") != buildinfo.Version || cmd.Form.Get("image") != "" {
			t.Fatal(cmd)
		}
	case <-ctx.Done():
		t.Fatal("No update relayed")
	}
	var metadata map[string]any
	json.Unmarshal(call(s.Handler(), "GET", "/api/hosts", "", "", &http.Cookie{Name: "qcu_session", Value: "browser"}).Body.Bytes(), &metadata)
	if metadata["version"] != "0.11.0" || metadata["updates_available"] != true {
		t.Fatal(metadata)
	}
}
