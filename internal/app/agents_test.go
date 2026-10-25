package app

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"reef/internal/agents"
	"reef/internal/docker"
	"strings"
	"testing"
	"time"
)

func TestAgentHTTPAndHostRouting(t *testing.T) {
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{AgentsEnabled: true}, "long-password-123456")
	s.agents, _ = agents.Open("")
	s.sessions[sessionKey("browser")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	machine := func(path string, body any, id, key string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(b))
		r.Header.Set("X-Reef-Host", id)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	token, _ := s.agents.Token("NAS")
	w := machine("/agent/v1/enroll", map[string]any{"token": token, "protocol": 1}, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var identity struct{ ID, Credential string }
	if e := json.Unmarshal(w.Body.Bytes(), &identity); e != nil {
		t.Fatal(e)
	}
	w = machine("/agent/v1/connect", map[string]any{"protocol": 1, "version": "test"}, identity.ID, identity.Credential)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var connected struct{ Generation string }
	json.Unmarshal(w.Body.Bytes(), &connected)
	w = machine("/agent/v1/inventory", agents.Update{Protocol: 1, Generation: connected.Generation, Sequence: 1, Containers: []docker.Container{{ID: strings.Repeat("a", 64), Name: "remote", State: "running"}}}, identity.ID, identity.Credential)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "/api/containers?host="+identity.ID, nil)
	r.Header.Set("Cookie", "qcu_session=browser")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "remote") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/container-logs", "/api/document", "/console"} {
		r = httptest.NewRequest("GET", path+"?host="+identity.ID+"&id="+strings.Repeat("a", 64), nil)
		r.Header.Set("Cookie", "qcu_session=browser")
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 409 {
			t.Fatal(path, w.Code)
		}
	}
	r = httptest.NewRequest("POST", "/api/action?host="+identity.ID, strings.NewReader("csrf=csrf&id=test&action=start"))
	r.Host = "reef.test"
	r.Header.Set("Origin", "http://reef.test")
	r.Header.Set("Cookie", "qcu_session=browser")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatal("remote action not rejected", w.Code)
	}
	r = httptest.NewRequest("POST", "/agent/v1/connect", strings.NewReader(`{"protocol":1}`))
	r.Header.Set("Cookie", "qcu_session=browser")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("browser cookie accepted", w.Code)
	}
}
