package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func persistentServer(t *testing.T, stateDir, password string) *Server {
	t.Helper()
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{StateDir: stateDir}, password)
	if e := s.restoreSessions(); e != nil {
		t.Fatal(e)
	}
	return s
}
func loginSession(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	w := call(s.Handler(), "POST", "/login", "password=long-password-123456", "http://reef.test", nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}

func TestSessionSurvivesRestartWithSameExpiryAndCSRF(t *testing.T) {
	stateDir := t.TempDir()
	first := persistentServer(t, stateDir, "long-password-123456")
	cookie := loginSession(t, first)
	before := first.sessions[sessionKey(cookie.Value)]
	path := filepath.Join(stateDir, "auth", "sessions.json")
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), cookie.Value) || strings.Contains(string(b), "long-password-123456") {
		t.Fatal("Session file contains a raw credential")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if dir.Mode().Perm() != 0700 {
		t.Fatal(dir.Mode())
	}
	restarted := persistentServer(t, stateDir, "long-password-123456")
	if w := call(restarted.Handler(), "GET", "/api/state", "", "", cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	after := restarted.sessions[sessionKey(cookie.Value)]
	if after.CSRF != before.CSRF || !after.Expires.Equal(before.Expires) {
		t.Fatal("Restart changed session identity or extended expiry")
	}
	if w := call(restarted.Handler(), "POST", "/logout", "csrf="+before.CSRF, "http://reef.test", cookie); w.Code != 303 {
		t.Fatal(w.Code)
	}
	again := persistentServer(t, stateDir, "long-password-123456")
	if w := call(again.Handler(), "GET", "/api/state", "", "", cookie); w.Code != 401 {
		t.Fatal("Logged-out cookie survived restart", w.Code)
	}
}

func TestPasswordChangeRevokesSessionsEvenWhenPasswordIsReverted(t *testing.T) {
	dir := t.TempDir()
	first := persistentServer(t, dir, "long-password-123456")
	cookie := loginSession(t, first)
	changed := persistentServer(t, dir, "another-long-password")
	if w := call(changed.Handler(), "GET", "/api/state", "", "", cookie); w.Code != 401 {
		t.Fatal(w.Code)
	}
	reverted := persistentServer(t, dir, "long-password-123456")
	if w := call(reverted.Handler(), "GET", "/api/state", "", "", cookie); w.Code != 401 {
		t.Fatal("Password reversion resurrected an old session", w.Code)
	}
}

func TestExpiredAndTamperedSessionsAreNotRestored(t *testing.T) {
	for _, damage := range []string{"expiry", "tampered signature", "invalid JSON", "oversized"} {
		t.Run(damage, func(t *testing.T) {
			dir := t.TempDir()
			s := persistentServer(t, dir, "long-password-123456")
			cookie := loginSession(t, s)
			path := filepath.Join(dir, "auth", "sessions.json")
			switch damage {
			case "expiry":
				key := sessionKey(cookie.Value)
				expired := s.sessions[key]
				expired.Expires = time.Now().Add(-time.Minute)
				s.sessions[key] = expired
				if e := s.saveSessions(s.sessions); e != nil {
					t.Fatal(e)
				}
			case "tampered signature":
				b, _ := os.ReadFile(path)
				var data signedSessionState
				if e := json.Unmarshal(b, &data); e != nil {
					t.Fatal(e)
				}
				data.Signature = strings.Repeat("0", 64)
				b, _ = json.Marshal(data)
				os.WriteFile(path, b, 0600)
			case "invalid JSON":
				os.WriteFile(path, []byte("not JSON"), 0600)
			case "oversized":
				os.WriteFile(path, []byte(strings.Repeat("x", 128*1024+1)), 0600)
			}
			restored := persistentServer(t, dir, "long-password-123456")
			if w := call(restored.Handler(), "GET", "/api/state", "", "", cookie); w.Code != 401 {
				t.Fatal(damage, w.Code)
			}
		})
	}
}

func TestSessionStorageFailureDoesNotIssueCookieOrPretendToSignOut(t *testing.T) {
	dir := t.TempDir()
	s := persistentServer(t, dir, "long-password-123456")
	cookie := loginSession(t, s)
	csrf := s.sessions[sessionKey(cookie.Value)].CSRF
	if e := os.Rename(filepath.Join(dir, "auth"), filepath.Join(dir, "auth-unavailable")); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "auth"), []byte("not a directory"), 0600); e != nil {
		t.Fatal(e)
	}
	w := call(s.Handler(), "POST", "/login", "password=long-password-123456", "http://reef.test", nil)
	if w.Code != 503 || len(w.Result().Cookies()) != 0 || len(s.sessions) != 1 {
		t.Fatal(w.Code, len(s.sessions))
	}
	w = call(s.Handler(), "POST", "/logout", "csrf="+csrf, "http://reef.test", cookie)
	if w.Code != 503 || len(w.Result().Cookies()) != 0 {
		t.Fatal("Sign-out failure was hidden", w.Code)
	}
	if w := call(s.Handler(), "GET", "/api/state", "", "", cookie); w.Code != 200 {
		t.Fatal("Failed transaction changed session state", w.Code)
	}
}

func TestSessionRegistryRejectsSymlinksAndBroadPermissions(t *testing.T) {
	dir := t.TempDir()
	s := persistentServer(t, dir, "long-password-123456")
	loginSession(t, s)
	path := filepath.Join(dir, "auth", "sessions.json")
	if e := os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if e := s.restoreSessions(); e == nil {
		t.Fatal("Public authentication state accepted")
	}
	os.Chmod(path, 0600)
	if e := os.Rename(path, path+".original"); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(path+".original", path); e != nil {
		t.Fatal(e)
	}
	if e := s.restoreSessions(); e == nil {
		t.Fatal("Authentication state symlink accepted")
	}
}

func TestConcurrentSessionChangesRemainPersisted(t *testing.T) {
	dir := t.TempDir()
	s := persistentServer(t, dir, "long-password-123456")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, e := s.createSession()
			if e != nil {
				t.Error(e)
				return
			}
			if e := s.removeSession(id); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	restored := persistentServer(t, dir, "long-password-123456")
	if len(restored.sessions) != 0 {
		t.Fatal("Concurrent sign-outs were lost", len(restored.sessions))
	}
}
