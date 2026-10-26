package app

import (
	"encoding/json"
	"net/http/httptest"
	"reef/internal/buildinfo"
	"testing"
)

func TestPublicHealthIdentifiesBuildWithoutAuthentication(t *testing.T) {
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{}, "long-password-123456")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	var result map[string]string
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if w.Code != 200 || len(result) != 3 || result["status"] != "ok" || result["version"] != buildinfo.Version || result["revision"] != buildinfo.Revision {
		t.Fatal(w.Code, result)
	}
	for method, want := range map[string]int{"HEAD": 200, "POST": 405, "DELETE": 405} {
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(method, "/healthz", nil))
		if w.Code != want {
			t.Fatal(method, w.Code)
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD has a body")
		}
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/state", nil))
	if w.Code != 401 {
		t.Fatal("Health bypassed API authentication")
	}
}
