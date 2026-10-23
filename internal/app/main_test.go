package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func call(h http.Handler, method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://reef.test"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAuthCSRFAndDocumentLock(t *testing.T) {
	f := &fakeExecutor{}
	m, p, rev := fixture(t, f)
	s := newServer(m, Config{}, "long-password-123456", false)
	h := s.Handler()
	if w := call(h, "GET", "/api/state", "", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(h, "POST", "/login", "password=long-password-123456", "http://evil.test", nil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call(h, "POST", "/login", "password=wrong", "http://reef.test", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := call(h, "POST", "/login", "password=long-password-123456", "http://reef.test", nil)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal(cookie)
	}
	sess := s.sessions[cookie.Value]
	body := url.Values{"id": {p.ID}, "action": {"save"}, "revision": {rev}, "content": {"services: {}\n"}, "csrf": {sess.CSRF}}.Encode()
	if w = call(h, "POST", "/api/action", body, "http://evil.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w = call(h, "POST", "/api/action", strings.Replace(body, sess.CSRF, "bad", 1), "http://reef.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w = call(h, "GET", "/api/action", "", "", cookie); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w = call(h, "POST", "/api/action", body, "http://reef.test", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w = call(h, "POST", "/api/action", body, "http://reef.test", cookie); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	m.wg.Wait()
	if w = call(h, "GET", "/api/document?id=unknown", "", "", cookie); w.Code != 404 {
		t.Fatal(w.Code)
	}
	mp := m.Projects[p.ID]
	mp.mu.Lock()
	mp.busy = true
	mp.mu.Unlock()
	if w = call(h, "GET", "/api/document?id="+p.ID, "", "", cookie); w.Code != 409 {
		t.Fatal(w.Code)
	}
	mp.mu.Lock()
	mp.busy = false
	mp.mu.Unlock()
	w = call(h, "GET", "/", "", "", cookie)
	if strings.Contains(w.Body.String(), "<script>alert") || !strings.Contains(w.Body.String(), "Reef") {
		t.Fatal("HTML incorrect")
	}
	if w.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("native same-origin forms require the same-origin referrer policy")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal("CSP absente")
	}
	if w = call(h, "POST", "/logout", url.Values{"csrf": {sess.CSRF}}.Encode(), "http://reef.test", cookie); w.Code != 303 {
		t.Fatal(w.Code)
	}
	if w = call(h, "GET", "/api/state", "", "", cookie); w.Code != 401 {
		t.Fatal("session persistante après logout")
	}
}
func TestLoginRateLimit(t *testing.T) {
	f := &fakeExecutor{}
	m, _, _ := fixture(t, f)
	s := newServer(m, Config{}, "a-long-password-value", false)
	for i := 0; i < 11; i++ {
		w := call(s.Handler(), "POST", "/login", "password=wrong", "http://reef.test", nil)
		if i == 10 && w.Code != 429 {
			t.Fatal(w.Code)
		}
	}
}
func TestConfigRejectsUnsafeAndDuplicate(t *testing.T) {
	f := &fakeExecutor{}
	_, p, _ := fixture(t, f)
	for _, mutate := range []func(*Config){func(c *Config) { c.Projects[0].ComposeFiles = []string{"../outside.yaml"} }, func(c *Config) { c.Projects = append(c.Projects, c.Projects[0]) }, func(c *Config) { c.Projects[0].ProjectName = "bad name" }, func(c *Config) { c.StateDir = "relative" }} {
		c := Config{StateDir: t.TempDir(), Projects: []Project{p}}
		mutate(&c)
		b, _ := json.Marshal(c)
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, b, 0600)
		if _, e := loadConfig(path); e == nil {
			t.Fatal("config dangereuse acceptée")
		}
	}
}

func TestOpaqueOriginExceptionOnlyInDemo(t *testing.T) {
	r := httptest.NewRequest("POST", "http://127.0.0.1:8099/login", nil)
	r.Header.Set("Origin", "null")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if (&Server{demo: false}).originOK(r) {
		t.Fatal("origine opaque acceptée en mode réel")
	}
	if !(&Server{demo: true}).originOK(r) {
		t.Fatal("navigateur intégré démo refusé")
	}
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if (&Server{demo: true}).originOK(r) {
		t.Fatal("origine tierce acceptée en démo")
	}
}

func TestValidConfiguration(t *testing.T) {
	f := &fakeExecutor{}
	_, p, _ := fixture(t, f)
	root, e := filepath.EvalSymlinks(p.Directory)
	if e != nil {
		t.Fatal(e)
	}
	p.Directory = root
	c := Config{StateDir: t.TempDir(), Projects: []Project{p}}
	b, _ := json.Marshal(c)
	path := filepath.Join(t.TempDir(), "config.json")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = loadConfig(path); e != nil {
		t.Fatal(e)
	}
}

func TestEnglishInterfaceAndMessages(t *testing.T) {
	f := &fakeExecutor{}
	m, p, rev := fixture(t, f)
	s := newServer(m, Config{}, "long-password-123456", true)
	h := s.Handler()
	login := call(h, "GET", "/login", "", "", nil)
	for _, text := range []string{`lang="en"`, "Your Compose projects, made simple.", "Password", "Open Reef", "Local simulation"} {
		if !strings.Contains(login.Body.String(), text) {
			t.Errorf("English login is missing %q", text)
		}
	}
	for _, tc := range []struct {
		method, path, body, origin, want string
		code                             int
	}{
		{"GET", "/api/state", "", "", "Sign-in required", 401},
		{"POST", "/login", "password=wrong", "http://reef.test", "Incorrect password", 401},
		{"POST", "/login", "password=wrong", "http://other.test", "Origin rejected", 403},
	} {
		w := call(h, tc.method, tc.path, tc.body, tc.origin, nil)
		if w.Code != tc.code || strings.TrimSpace(w.Body.String()) != tc.want {
			t.Errorf("English error: got %d %q; want %d %q", w.Code, w.Body.String(), tc.code, tc.want)
		}
	}
	w := call(h, "POST", "/login", "password=long-password-123456", "http://reef.test", nil)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	page := call(h, "GET", "/", "", "", w.Result().Cookies()[0]).Body.String()
	for _, text := range []string{`lang="en"`, "Sign out", "Apply update", "Save file", "Start / retry", "Stop project", "Confirm operation", "Cancel", "Latest operation", "Data folders stay in place."} {
		if !strings.Contains(page, text) {
			t.Errorf("English editor is missing %q", text)
		}
	}
	launch(t, m, p, rev, "save")
	op := m.Projects[p.ID].Op
	if op.Phase != "completed" || op.Result != "Operation completed." || !strings.Contains(op.Log, "File saved; a backup of the previous version was created beside the Compose file.") {
		t.Fatal("operation status or log is not in English", op)
	}
}
