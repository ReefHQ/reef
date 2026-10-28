package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reef/internal/agents"
	"reef/internal/history"
	"reef/internal/notifications"
	"strings"
	"testing"
	"time"
)

func historyServer(t *testing.T) (*Server, string) {
	t.Helper()
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{AgentsEnabled: true}, "password-long-enough")
	dir := t.TempDir()
	var err error
	s.history, err = history.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.webhooks = notificationQueue(t, false)
	s.m.Notify = s.recordEvent
	s.agents, err = agents.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.sessions[sessionKey("cookie")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	return s, dir
}
func drainHistory(s *Server) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.history.Run(ctx)
}
func TestHistoryRecordsActualActionsWithoutEnabledWebhookAndFailureDoesNotFailDocker(t *testing.T) {
	s, _ := historyServer(t)
	s.m.Images = &ImageService{Exec: volumeFixture()}
	if _, err := s.m.beginVolumes("create", "new-data", ""); err != nil {
		t.Fatal(err)
	}
	s.m.wg.Wait()
	drainHistory(s)
	page, err := s.history.Query(history.Query{Host: "local"})
	if err != nil || page.Total != 1 || page.Rows[0].Scope != "volume" || page.Rows[0].Status != "completed" {
		t.Fatal(page, err)
	}
	if s.webhooks.View().Pending != 0 {
		t.Fatal("disabled webhook was queued")
	}
	s.history = history.Unavailable()
	if _, err = s.m.beginVolumes("create", "more-data", ""); err != nil {
		t.Fatal("history failure blocked Docker", err)
	}
	s.m.wg.Wait()
	op := s.m.Images.volumeOperation()
	if len(op.Results) != 1 || !op.Results[0].OK {
		t.Fatal(op)
	}
	page, err = s.history.Query(history.Query{Host: "all"})
	if err != nil || !page.PersistenceError || page.Dropped != 2 {
		t.Fatal(page, err)
	}
}
func TestHistoryAPIIsCentralAuthenticatedReadOnlyAndKeepsWebhookSecretsOut(t *testing.T) {
	s, dir := historyServer(t)
	cookie := &http.Cookie{Name: "qcu_session", Value: "cookie"}
	h := s.Handler()
	if w := call(h, "GET", "/api/history?host=all", "", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, tc := range []struct {
		method, path, body, origin string
		code                       int
	}{
		{"POST", "/api/history", "csrf=csrf", "http://evil.test", 403}, {"POST", "/api/history", "csrf=csrf", "http://reef.test", 405}, {"GET", "/api/history?host=../../state", "", "", 400}, {"GET", "/api/history?offset=1", "", "", 400},
	} {
		if w := call(h, tc.method, tc.path, tc.body, tc.origin, cookie); w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	body := "csrf=csrf&confirmed=yes&action=save&enabled=true&format=json&url=https%3A%2F%2Freceiver.invalid%2Fprivate-webhook-token"
	if w := call(h, "POST", "/api/webhook", body, "http://reef.test", cookie); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	drainHistory(s)
	w := call(h, "GET", "/api/history?host=all", "", "", cookie)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-webhook-token") || !strings.Contains(w.Body.String(), "Webhook settings") {
		t.Fatal(w.Code, w.Body.String())
	}
	raw, err := os.ReadFile(filepath.Join(dir, "history", "actions.json"))
	if err != nil || strings.Contains(string(raw), "private-webhook-token") {
		t.Fatal(err, string(raw))
	}
	if agents.AllowedCommand("GET", "/api/history") {
		t.Fatal("agents can read the central history endpoint")
	}
}
func eventPeer(t *testing.T, s *Server) (string, string, string) {
	t.Helper()
	token, err := s.agents.Token("Remote")
	if err != nil {
		t.Fatal(err)
	}
	id, key, err := s.agents.Enroll(token, agents.Protocol)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := s.agents.Begin(id, key, "0.14.0", agents.Protocol)
	if err != nil {
		t.Fatal(err)
	}
	s.agents.Control(id, gen, true)
	s.agents.Events(id, gen, true)
	return id, key, gen
}
func postHistoryEvent(s *Server, host, key, gen string, e notifications.Event) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(map[string]any{"generation": gen, "events": []notifications.Event{e}})
	r := httptest.NewRequest("POST", "/agent/v1/events", bytes.NewReader(raw))
	r.Header.Set("X-Reef-Host", host)
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestHistoryRemoteIngestionWorksWithoutNotificationStorageAndBindsRegistryHost(t *testing.T) {
	s, _ := historyServer(t)
	s.webhooks = notifications.Unavailable(false)
	id, key, gen := eventPeer(t, s)
	e := notifications.New("project", "stop", "completed", randomID(), "remote-app")
	e.HostID = "local"
	e.Host = "Forged"
	for _, tc := range []struct {
		key, gen string
		want     int
	}{{"wrong", gen, 403}, {key, "old-generation", 403}, {key, gen, 200}, {key, gen, 200}} {
		w := postHistoryEvent(s, id, tc.key, tc.gen, e)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	page, err := s.history.Query(history.Query{Host: id})
	if err != nil || page.Total != 1 || page.RetainedEvents != 1 || page.Rows[0].Host != "Remote" {
		t.Fatal(page, err)
	}
	s.agents.Change(id, "", true)
	cookie := &http.Cookie{Name: "qcu_session", Value: "cookie"}
	w := call(s.Handler(), "GET", "/api/history?host="+id, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "remote-app") {
		t.Fatal("disconnected history unavailable", w.Code, w.Body.String())
	}
}
func TestIndependentHistoryAndWebhookFailuresDeduplicateRetries(t *testing.T) {
	for _, failed := range []string{"history", "webhook"} {
		t.Run(failed, func(t *testing.T) {
			s, dir := historyServer(t)
			webhookDir := t.TempDir()
			s.webhooks, _ = notifications.Open(webhookDir, false)
			s.webhooks.Configure(notifications.Config{Enabled: true, Format: "json", URL: "https://receiver.invalid/private-token"}, false)
			s.history.RecordLocal(notifications.New("project", "save", "completed", randomID(), "seed"))
			drainHistory(s)
			id, key, gen := eventPeer(t, s)
			e := notifications.New("project", "stop", "completed", randomID(), "remote-app")
			var path string
			var old []byte
			if failed == "history" {
				path = filepath.Join(dir, "history", "actions.json")
				old, _ = os.ReadFile(path)
				os.WriteFile(path, []byte("private-corrupt-history"), 0600)
			} else {
				path = filepath.Join(webhookDir, "notifications", "state.json")
				old, _ = os.ReadFile(path)
				os.Remove(path)
				os.Mkdir(path, 0700)
			}
			if w := postHistoryEvent(s, id, key, gen, e); w.Code != 503 {
				t.Fatal("failed sink was acknowledged", w.Code, w.Body.String())
			}
			if failed == "webhook" {
				os.Remove(path)
			}
			if err := os.WriteFile(path, old, 0600); err != nil {
				t.Fatal(err)
			}
			if w := postHistoryEvent(s, id, key, gen, e); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			page, err := s.history.Query(history.Query{Host: id})
			if err != nil || page.Total != 1 {
				t.Fatal(page, err)
			}
			if s.webhooks.View().Pending != 1 {
				t.Fatal("webhook duplicated", s.webhooks.View())
			}
		})
	}
}

func TestHistoryAdvertisesRemoteEventsWhenWebhookStorageIsUnavailable(t *testing.T) {
	s, _ := historyServer(t)
	s.webhooks = notifications.Unavailable(false)
	token, err := s.agents.Token("Remote")
	if err != nil {
		t.Fatal(err)
	}
	id, key, err := s.agents.Enroll(token, agents.Protocol)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"protocol": agents.Protocol, "version": "0.14.0", "capabilities": []string{agents.ControlCapability, agents.EventsCapability}})
	r := httptest.NewRequest("POST", "/agent/v1/connect", bytes.NewReader(raw))
	r.Header.Set("X-Reef-Host", id)
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var reply struct {
		Generation string
		Events     bool
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || !reply.Events || !s.agents.CanControl(id, reply.Generation) || !s.agents.CanEvents(id) {
		t.Fatal("history event transport not enabled", w.Code, w.Body.String())
	}
}
