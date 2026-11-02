package app

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"reef/internal/agents"
	"reef/internal/history"
	"reef/internal/notifications"
	"reflect"
	"strings"
	"testing"
)

func TestWebhookCategoryAPIKeepsLegacyPreferencesHistoryAndExplicitTest(t *testing.T) {
	s, _, cookie, csrf := userServer(t)
	form := url.Values{"action": {"save"}, "enabled": {"true"}, "format": {"json"}, "url": {"https://receiver.invalid/fixture-private-token"}, "csrf": {csrf}, "confirmed": {"yes"}, "categories_present": {"yes"}, "categories": {"project"}}
	post := func(values url.Values) int {
		return call(s.Handler(), "POST", "/api/webhook", values.Encode(), "http://reef.test", cookie).Code
	}
	if status := post(form); status != 200 {
		t.Fatal(status)
	}
	if !reflect.DeepEqual(s.webhooks.View().Categories, []string{"project"}) {
		t.Fatal(s.webhooks.View())
	}
	s.recordEvent(notifications.New("image", "pull", "completed", "", "image"))
	s.recordEvent(notifications.New("project", "restart", "completed", "", "project"))
	if s.webhooks.View().Pending != 1 {
		t.Fatal(s.webhooks.View())
	}
	drainHistory(s)
	page, err := s.history.Query(history.Query{Host: "local", Scope: "image"})
	if err != nil || len(page.Rows) != 1 {
		t.Fatal("Filtered event missing from History", page, err)
	}
	form.Del("categories")
	form.Del("url")
	if post(form) != 200 || len(s.webhooks.View().Categories) != 0 {
		t.Fatal("Explicit empty selection not saved")
	}
	form.Del("categories_present")
	if post(form) != 200 || len(s.webhooks.View().Categories) != 0 {
		t.Fatal("Legacy save reset the filter")
	}
	test := url.Values{"action": {"test"}, "csrf": {csrf}, "confirmed": {"yes"}}
	if post(test) != 200 || s.webhooks.View().Pending != 1 || s.webhooks.Batch()[0].Action != "test" {
		t.Fatal("Explicit test was filtered")
	}
	form.Set("categories_present", "yes")
	form.Set("categories", "not-a-category")
	if post(form) != 400 || len(s.webhooks.View().Categories) != 0 {
		t.Fatal("Invalid selection accepted")
	}
	form.Set("categories", "project")
	form.Set("csrf", "wrong")
	if post(form) != 403 {
		t.Fatal("Missing CSRF protection")
	}
	reader := addReader(t, s, "viewer", "local")
	readerCookie, readerCSRF := userCookie(t, s, reader)
	form.Set("csrf", readerCSRF)
	if w := call(s.Handler(), "POST", "/api/webhook", form.Encode(), "http://reef.test", readerCookie); w.Code != 403 {
		t.Fatal("Viewer changed webhook categories", w.Code)
	}
	w := call(s.Handler(), "GET", "/api/webhook", "", "", cookie)
	if w.Code != 200 || strings.Contains(w.Body.String(), "fixture-private-token") || !strings.Contains(w.Body.String(), "category_options") {
		t.Fatal(w.Code, w.Body)
	}
}
func TestFilteredRemoteWebhookEventsAreAckedAndStillRecordedInHistory(t *testing.T) {
	s, _ := historyServer(t)
	if err := s.webhooks.Configure(notifications.Config{Enabled: true, Format: "json", URL: "https://receiver.invalid", Categories: []string{}}, false); err != nil {
		t.Fatal(err)
	}
	token, _ := s.agents.Token("Remote")
	id, key, _ := s.agents.Enroll(token, agents.Protocol)
	gen, _ := s.agents.Begin(id, key, "0.29.0", agents.Protocol)
	s.agents.Control(id, gen, true)
	s.agents.Events(id, gen, true)
	e := notifications.New("image", "pull", "completed", randomID(), "app")
	for i := 0; i < 2; i++ {
		raw, _ := json.Marshal(map[string]any{"generation": gen, "events": []notifications.Event{e}})
		r := httptest.NewRequest("POST", "/agent/v1/events", bytes.NewReader(raw))
		r.Header.Set("X-Reef-Host", id)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), e.ID) {
			t.Fatal(w.Code, w.Body)
		}
	}
	if s.webhooks.View().Pending != 0 || s.webhooks.View().Dropped != 0 {
		t.Fatal(s.webhooks.View())
	}
	page, err := s.history.Query(history.Query{Host: id, Scope: "image"})
	if err != nil || len(page.Rows) != 1 {
		t.Fatal("Remote history missing or duplicated", page, err)
	}
}
