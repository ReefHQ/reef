package app

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reef/internal/accounts"
	"reef/internal/agents"
	"strings"
	"testing"
	"time"
)

func TestLoginDurationIsExplicitAndCookieMatchesStoredExpiry(t *testing.T) {
	for _, tc := range []struct {
		name, path, form string
		lifetime         time.Duration
	}{
		{"default", "/login", "", sessionLifetime},
		{"remember", "/login", "&remember=true", rememberedSessionLifetime},
		{"query cannot opt in", "/login?remember=true", "", sessionLifetime},
		{"unknown option", "/login", "&remember=forever", sessionLifetime},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := persistentServer(t, t.TempDir(), "long-password-123456")
			s.cfg.SecureCookie = true
			before := time.Now()
			w := call(s.Handler(), "POST", tc.path, "password=long-password-123456"+tc.form, "https://reef.test", nil)
			if w.Code != 303 || len(w.Result().Cookies()) != 1 {
				t.Fatal(w.Code, w.Body.String())
			}
			c := w.Result().Cookies()[0]
			sess := s.sessions[sessionKey(c.Value)]
			if c.Name != s.sessionCookieName() || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
				t.Fatal("Cookie scope/protections changed", c)
			}
			if c.MaxAge < int(tc.lifetime.Seconds())-1 || c.MaxAge > int(tc.lifetime.Seconds()) || sess.Expires.Before(before.Add(tc.lifetime)) || sess.Expires.After(time.Now().Add(tc.lifetime)) || !c.Expires.Equal(sess.Expires.UTC().Truncate(time.Second)) {
				t.Fatal("Cookie and registry disagree about lifetime", c.MaxAge, sess.Expires, c.Expires)
			}
			for _, elapsed := range []time.Duration{13 * time.Hour, 31 * 24 * time.Hour} {
				// Shift just this fixture's expiry to test the real access gate without sleeping.
				sess.Expires = before.Add(tc.lifetime - elapsed)
				s.sessions[sessionKey(c.Value)] = sess
				got := call(s.Handler(), "GET", "/api/state", "", "", c).Code
				want := 401
				if tc.lifetime > elapsed {
					want = 200
				}
				if got != want {
					t.Fatal("Unexpected access after elapsed time", elapsed, got, want)
				}
			}
		})
	}
}

func TestRememberedAccountSurvivesRestartAndRevocationStillWins(t *testing.T) {
	for _, action := range []string{"logout", "revoke", "password", "disable", "scope"} {
		t.Run(action, func(t *testing.T) {
			s, user, _, _ := userServer(t)
			// Discard the history fixture's deliberately non-production CSRF token.
			s.sessions = map[string]session{}
			w := call(s.Handler(), "POST", "/login", "username=admin&password=long-password-123456&remember=true", "http://reef.test", nil)
			if w.Code != 303 {
				t.Fatal(w.Code)
			}
			c := w.Result().Cookies()[0]
			sess := s.sessions[sessionKey(c.Value)]
			if time.Until(sess.Expires) < 29*24*time.Hour {
				t.Fatal("Remember choice ignored")
			}
			restarted := newServer(s.m, s.cfg, "different-bootstrap-password")
			var e error
			restarted.accounts, e = accounts.Open(s.cfg.StateDir, "different-bootstrap-password")
			if e != nil {
				t.Fatal(e)
			}
			if e = restarted.restoreSessions(); e != nil {
				t.Fatal(e)
			}
			status := call(restarted.Handler(), "GET", "/api/state", "", "", c).Code
			if restarted.sessionCookieName() != c.Name || status != 200 || !restarted.sessions[sessionKey(c.Value)].Expires.Equal(sess.Expires) {
				t.Fatal("Restart changed a remembered session", "same name", restarted.sessionCookieName() == c.Name, "status", status, "same expiry", restarted.sessions[sessionKey(c.Value)].Expires.Equal(sess.Expires))
			}
			s = restarted
			switch action {
			case "logout":
				w = call(s.Handler(), "POST", "/logout", "csrf="+sess.CSRF, "http://reef.test", c)
				if w.Code != 303 || w.Result().Cookies()[0].Name != c.Name || w.Result().Cookies()[0].MaxAge != -1 {
					t.Fatal("Logout did not clear its own cookie", w.Code)
				}
			case "revoke":
				_, e = s.accounts.Change(user, "", false)
			case "password":
				e = s.accounts.ChangePassword(user.ID, "long-password-123456", "new-long-password-123456")
			case "disable", "scope":
				// Keep another global admin before narrowing or disabling this one.
				_, e = s.accounts.Change(accounts.User{Username: "other-admin", Role: "admin", AllHosts: true}, "long-password-123456", false)
				if e != nil {
					t.Fatal(e)
				}
				if action == "disable" {
					user.Disabled = true
				} else {
					user.AllHosts = false
					user.Hosts = []string{"local"}
				}
				_, e = s.accounts.Change(user, "", false)
			}
			if e != nil {
				t.Fatal(e)
			}
			if call(s.Handler(), "GET", "/api/state", "", "", c).Code != 401 {
				t.Fatal("Remembered session bypassed revocation")
			}
			if e = s.restoreSessions(); e != nil {
				t.Fatal(e)
			}
			if call(s.Handler(), "GET", "/api/state", "", "", c).Code != 401 {
				t.Fatal("Revoked session resurrected after reload")
			}
		})
	}
}

func TestDifferentInstallationsDoNotOverwriteCookiesOnSameHostname(t *testing.T) {
	a, _, _, _ := userServer(t)
	b, _, _, _ := userServer(t)
	if a.sessionCookieName() == b.sessionCookieName() {
		t.Fatal("Cookie names are shared across installations")
	}
	jar, _ := cookiejar.New(nil)
	visit := func(s *Server, origin, method, path, body string) *httptest.ResponseRecorder {
		u, _ := url.Parse(origin + path)
		r := httptest.NewRequest(method, u.String(), strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		for _, c := range jar.Cookies(u) {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		jar.SetCookies(u, w.Result().Cookies())
		return w
	}
	for _, item := range []struct {
		s      *Server
		origin string
	}{{a, "http://reef.test:8099"}, {b, "http://reef.test:9099"}} {
		if w := visit(item.s, item.origin, "POST", "/login", "username=admin&password=long-password-123456&remember=true"); w.Code != 303 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, item := range []struct {
		s      *Server
		origin string
	}{{a, "http://reef.test:8099"}, {b, "http://reef.test:9099"}} {
		if w := visit(item.s, item.origin, "GET", "/api/state", ""); w.Code != 200 {
			t.Fatal("Another installation displaced login", w.Code)
		}
	}
	u, _ := url.Parse("http://reef.test:8099")
	var token string
	for _, c := range jar.Cookies(u) {
		if c.Name == a.sessionCookieName() {
			token = c.Value
		}
	}
	csrf := a.sessions[sessionKey(token)].CSRF
	if w := visit(a, u.String(), "POST", "/logout", "csrf="+csrf); w.Code != 303 {
		t.Fatal(w.Code)
	}
	if visit(a, u.String(), "GET", "/api/state", "").Code != 401 || visit(b, "http://reef.test:9099", "GET", "/api/state", "").Code != 200 {
		t.Fatal("Logout affected the other installation")
	}
}

func TestLegacyCookieMigratesWithoutExtendingExpiryAndCannotResurrect(t *testing.T) {
	s := persistentServer(t, t.TempDir(), "long-password-123456")
	id, e := s.createSession()
	if e != nil {
		t.Fatal(e)
	}
	legacy := &http.Cookie{Name: "qcu_session", Value: id}
	sess := s.sessions[sessionKey(id)]
	w := call(s.Handler(), "GET", "/api/state", "", "", legacy)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatal(w.Code)
	}
	current := w.Result().Cookies()[0]
	if current.Name != s.sessionCookieName() || current.Value != id || !current.Expires.Equal(sess.Expires.UTC().Truncate(time.Second)) {
		t.Fatal("Legacy migration changed identity or expiry")
	}
	r := httptest.NewRequest("GET", "http://reef.test/api/state", nil)
	r.AddCookie(&http.Cookie{Name: s.sessionCookieName(), Value: "invalid-current"})
	r.AddCookie(legacy)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("Invalid current cookie fell back to an older account")
	}
	// Logging in again replaces all valid cookies belonging to this browser.
	w = call(s.Handler(), "POST", "/login", "password=long-password-123456&remember=true", "http://reef.test", legacy)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	current = w.Result().Cookies()[0]
	if call(s.Handler(), "GET", "/api/state", "", "", legacy).Code != 401 {
		t.Fatal("Replaced legacy session remains valid")
	}
	csrf := s.sessions[sessionKey(current.Value)].CSRF
	if call(s.Handler(), "POST", "/logout", "csrf="+csrf, "http://reef.test", current).Code != 303 {
		t.Fatal("Logout failed")
	}
	if e = s.restoreSessions(); e != nil {
		t.Fatal(e)
	}
	for _, c := range []*http.Cookie{legacy, current} {
		if call(s.Handler(), "GET", "/api/state", "", "", c).Code != 401 {
			t.Fatal("Old login resurrected")
		}
	}
}

func TestFailedRememberedLoginPreservesChoiceWithoutIssuingCredentials(t *testing.T) {
	s := persistentServer(t, t.TempDir(), "long-password-123456")
	w := call(s.Handler(), "POST", "/login", "username=someone&password=private-wrong-value&remember=true", "http://reef.test", nil)
	if w.Code != 401 || len(w.Result().Cookies()) != 0 || len(s.sessions) != 0 || strings.Contains(w.Body.String(), "private-wrong-value") || !strings.Contains(w.Body.String(), `value="true" checked`) || !strings.Contains(w.Body.String(), "Remember me for 30 days") {
		t.Fatal("Failed login lost choice or issued credentials", w.Code)
	}
	w = call(s.Handler(), "GET", "/login", "", "", nil)
	if strings.Contains(w.Body.String(), `value="true" checked`) {
		t.Fatal("Remember me should default off")
	}
}

func TestRememberedLoginUsesExistingAgentGrantLimit(t *testing.T) {
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{AgentsEnabled: true}, "long-password-123456")
	s.agents, _ = agents.Open(t.TempDir())
	id, e := s.createLoginSession(accounts.User{}, true, nil)
	if e != nil {
		t.Fatal(e)
	}
	remote, _, _ := fixture(t, &fakeExecutor{})
	// This backend retains the existing 12-hour maximum command lifetime.
	backend := newServer(remote, Config{}, "unused-password")
	host, _ := relayAgentFixture(t, s, backend, "remembered-remote")
	c := &http.Cookie{Name: s.sessionCookieName(), Value: id}
	w := call(s.Handler(), "GET", "/api/state?host="+host, "", "", c)
	if w.Code != 200 {
		t.Fatal("Remembered session failed against existing agent protocol", w.Code, w.Body.String())
	}
	if time.Until(s.sessions[sessionKey(id)].Expires) < 29*24*time.Hour {
		t.Fatal("Remote dispatch shortened the browser session")
	}
}
