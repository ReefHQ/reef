package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reef/internal/agents"
	"reef/internal/notifications"
	"strings"
	"testing"
	"time"
)

func notificationQueue(t *testing.T, relay bool) *notifications.Service {
	t.Helper()
	s, err := notifications.Open(t.TempDir(), relay)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestWebhookSettingsRequireAuthenticationConfirmationAndKeepURLPrivate(t *testing.T) {
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{}, "long-password-123456")
	s.webhooks = notificationQueue(t, false)
	s.sessions[sessionKey("browser")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "browser"}
	form := url.Values{"csrf": {"csrf"}, "action": {"save"}, "confirmed": {"yes"}, "enabled": {"true"}, "format": {"json"}, "url": {"https://receiver.invalid/private-token?secret=private-query"}}.Encode()
	for _, tc := range []struct {
		body, origin string
		cookie       *http.Cookie
		want         int
	}{
		{form, "http://reef.test", nil, 401}, {form, "http://evil.test", cookie, 403},
		{strings.Replace(form, "csrf=csrf", "csrf=bad", 1), "http://reef.test", cookie, 403},
		{strings.Replace(form, "confirmed=yes", "confirmed=no", 1), "http://reef.test", cookie, 400},
	} {
		w := call(s.Handler(), "POST", "/api/webhook", tc.body, tc.origin, tc.cookie)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := call(s.Handler(), "POST", "/api/webhook", form, "http://reef.test", cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, method := range []string{"GET", "POST"} {
		body := ""
		origin := ""
		if method == "POST" {
			body = "csrf=csrf&confirmed=yes&action=test"
			origin = "http://reef.test"
		}
		w = call(s.Handler(), method, "/api/webhook", body, origin, cookie)
		if w.Code != 200 || strings.Contains(w.Body.String(), "private-token") || strings.Contains(w.Body.String(), "private-query") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if !s.webhooks.View().Enabled || s.webhooks.View().Pending != 2 {
		t.Fatal(s.webhooks.View())
	}
	if agents.AllowedCommand("GET", "/api/webhook") || agents.AllowedCommand("POST", "/api/webhook") {
		t.Fatal("Webhook destination can be relayed to an agent")
	}
}
func TestProjectActionEventsContainMetadataWithoutComposeLogsPathsOrSecrets(t *testing.T) {
	for _, failure := range []string{"", "pull"} {
		t.Run(failure, func(t *testing.T) {
			f := &fakeExecutor{fail: failure, output: "untrusted application output secret-value secret-custom"}
			m, p, rev := fixture(t, f)
			queue := notificationQueue(t, true)
			m.Notify = queue.Emit
			launch(t, m, p, rev, "apply")
			events := queue.Batch()
			if len(events) != 2 || events[0].Status != "started" || events[1].Operation != events[0].Operation {
				t.Fatal(events)
			}
			want := "completed"
			if failure != "" {
				want = "failed"
			}
			if events[1].Status != want {
				t.Fatal(events)
			}
			b, _ := json.Marshal(events)
			for _, secret := range []string{"secret-value", "secret-custom", "untrusted application output", p.Directory, "services:", "injected failure"} {
				if strings.Contains(string(b), secret) {
					t.Fatal("Private operation output escaped into a notification")
				}
			}
		})
	}
}
func TestCatalogAndBuildEventsReportRealCompletionCounts(t *testing.T) {
	x := catalogFixture()
	m, _, _ := fixture(t, &fakeExecutor{})
	m.Images = &ImageService{Exec: x}
	queue := notificationQueue(t, true)
	m.Notify = queue.Emit
	if _, err := m.beginCatalog("pull", "example/app:latest", "", ""); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	events := queue.Batch()
	if len(events) != 2 || events[1].Scope != "image" || events[1].Succeeded != 1 || events[1].Count != 1 {
		t.Fatal(events)
	}
	v := volumeFixture()
	m.Images = &ImageService{Exec: v}
	if _, err := m.beginVolumes("create", "new-data", ""); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	events = queue.Batch()
	if len(events) != 4 || events[3].Scope != "volume" || events[3].Succeeded != 1 || events[3].Target != "new-data" {
		t.Fatal(events)
	}
	bm, p, rev, _ := projectBuildFixture(t)
	bq := notificationQueue(t, true)
	bm.Notify = bq.Emit
	if err := bm.beginOptions(p.ID, "build", rev, "", BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	bm.wg.Wait()
	if events = bq.Batch(); len(events) != 2 || events[1].Scope != "build" || events[1].Status != "completed" {
		t.Fatal(events)
	}
}
func TestRemoteEventProtocolAuthenticatesGenerationBindsHostAndDeduplicates(t *testing.T) {
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{AgentsEnabled: true}, "long-password-123456")
	s.agents, _ = agents.Open("")
	s.webhooks = notificationQueue(t, false)
	s.webhooks.Configure(notifications.Config{Enabled: true, Format: "json", URL: "https://receiver.invalid"}, false)
	token, _ := s.agents.Token("Ego")
	id, key, _ := s.agents.Enroll(token, agents.Protocol)
	gen, _ := s.agents.Begin(id, key, "0.11.0", agents.Protocol)
	s.agents.Control(id, gen, true)
	s.agents.Events(id, gen, true)
	e := notifications.New("project", "stop", "completed", randomID(), "web")
	e.HostID = "forged-local"
	e.Host = "Wrong name"
	post := func(generation, credential string, events []notifications.Event) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]any{"generation": generation, "events": events})
		r := httptest.NewRequest("POST", "/agent/v1/events", bytes.NewReader(b))
		r.Header.Set("X-Reef-Host", id)
		r.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := post(gen, "wrong", []notifications.Event{e}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := post("old-generation", key, []notifications.Event{e}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for i := 0; i < 2; i++ {
		w := post(gen, key, []notifications.Event{e})
		if w.Code != 200 || !strings.Contains(w.Body.String(), e.ID) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if s.webhooks.View().Pending != 1 {
		t.Fatal("Replayed events were duplicated")
	}
	queued := s.webhooks.Batch()
	if queued[0].HostID != id || queued[0].Host != "Ego" {
		t.Fatal(queued)
	}
	if w := post(gen, key, make([]notifications.Event, 33)); w.Code != 400 {
		t.Fatal("Oversized batch accepted", w.Code)
	}
	next, _ := s.agents.Begin(id, key, "0.11.0", agents.Protocol)
	s.agents.Control(id, next, true)
	if s.agents.CanEvents(id) {
		t.Fatal("Event capability survived generation replacement")
	}
	s.agents.Change(id, "", true)
	if w := post(gen, key, []notifications.Event{e}); w.Code != 403 {
		t.Fatal("Revoked host can publish", w.Code)
	}
}
func TestBackendOutboxPersistsAcrossAgentReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	backend, err := NewAgentBackend("", root, "/missing-docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	e := notifications.New("agent", "update", "started", randomID(), "0.11.0")
	backend.server.m.emit(e)
	backend.Close()
	backend, err = NewAgentBackend("", root, "/missing-docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	if got := backend.ActionEvents(); len(got) != 1 || got[0].ID != e.ID {
		t.Fatal(got)
	}
	if err = backend.AckActionEvents([]string{e.ID}); err != nil || len(backend.ActionEvents()) != 0 {
		t.Fatal(err)
	}
}

func TestConsoleLifecycleEventsAreBoundToContainer(t *testing.T) {
	s, f, cookie := consoleFixture(t)
	queue := notificationQueue(t, true)
	s.m.Notify = queue.Emit
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "http://reef.test/api/console/open", strings.NewReader(consoleForm().Encode())).WithContext(ctx)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://reef.test")
	r.AddCookie(cookie)
	done := make(chan struct{})
	go func() { s.Handler().ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	deadline := time.After(2 * time.Second)
	for len(queue.Batch()) < 1 {
		select {
		case <-deadline:
			t.Fatal("No console start event")
		case <-time.After(time.Millisecond):
		}
	}
	f.terminal.Close()
	cancel()
	<-done
	events := queue.Batch()
	if len(events) < 2 {
		t.Fatal(events)
	}
	for _, event := range events {
		if event.Scope != "console" || event.Target != "fixture" {
			t.Fatal(event)
		}
	}
}

func TestCorruptNotificationOutboxDoesNotDisableAgentControl(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	backend, err := NewAgentBackend("", root, "/missing-docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	backend.server.m.emit(notifications.New("agent", "update", "started", randomID(), "0.11.0"))
	backend.Close()
	if err = os.WriteFile(filepath.Join(root, "notifications", "state.json"), []byte("corrupt fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	backend, err = NewAgentBackend("", root, "/missing-docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	if backend.HasActionEvents() {
		t.Fatal("Corrupt journal advertised event capture")
	}
	backend.server.m.emit(notifications.New("project", "save", "completed", "", "web"))
	if len(backend.ActionEvents()) != 0 {
		t.Fatal("Unavailable journal accepted events")
	}
}
