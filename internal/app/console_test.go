package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"reef/internal/docker"
)

type consoleStub struct {
	inventoryStub
	opens    int
	terminal *fakeConsole
}

func (f *consoleStub) OpenConsole(context.Context, string, string, int, int) (docker.Console, error) {
	f.opens++
	return f.terminal, nil
}

type fakeConsole struct {
	r          *io.PipeReader
	w          *io.PipeWriter
	mu         sync.Mutex
	input      bytes.Buffer
	cols, rows int
	closed     chan struct{}
	once       sync.Once
}

func newFakeConsole() *fakeConsole {
	r, w := io.Pipe()
	return &fakeConsole{r: r, w: w, closed: make(chan struct{})}
}
func (f *fakeConsole) Read(b []byte) (int, error) { return f.r.Read(b) }
func (f *fakeConsole) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.input.Write(b)
}
func (f *fakeConsole) Resize(_ context.Context, c, r int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cols = c
	f.rows = r
	return nil
}
func (f *fakeConsole) Close() error {
	f.once.Do(func() { f.r.Close(); f.w.Close(); close(f.closed) })
	return nil
}

func consoleFixture(t *testing.T) (*Server, *consoleStub, *http.Cookie) {
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{}, "long-password-123456")
	f := &consoleStub{inventoryStub: inventoryStub{rows: []docker.Container{{ID: strings.Repeat("a", 64), Name: "fixture", State: "running"}}}, terminal: newFakeConsole()}
	s.discovery = f
	s.sessions["console-test"] = session{CSRF: "owner", Expires: time.Now().Add(time.Hour)}
	s.sessions["other-test"] = session{CSRF: "other", Expires: time.Now().Add(time.Hour)}
	t.Cleanup(func() { s.closeConsoles(); f.terminal.Close() })
	return s, f, &http.Cookie{Name: "qcu_session", Value: "console-test"}
}
func consoleForm() url.Values {
	return url.Values{"csrf": {"owner"}, "id": {strings.Repeat("a", 64)}, "shell": {"/bin/sh"}, "cols": {"80"}, "rows": {"24"}, "confirmed": {"yes"}}
}

func TestConsoleAuthorizationAndAdmission(t *testing.T) {
	s, f, cookie := consoleFixture(t)
	s.cfg.ConsoleSessionsPerLogin = 2
	s.cfg.ConsoleSessionsTotal = 4
	h := s.Handler()
	form := consoleForm()
	if w := call(h, "POST", "/api/console/open", form.Encode(), "http://reef.test", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(h, "POST", "/api/console/open", form.Encode(), "http://evil.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	form.Set("csrf", "bad")
	if w := call(h, "POST", "/api/console/open", form.Encode(), "http://reef.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	form.Set("csrf", "owner")
	form.Set("confirmed", "no")
	if w := call(h, "POST", "/api/console/open", form.Encode(), "http://reef.test", cookie); w.Code != 400 {
		t.Fatal(w.Code)
	}
	form.Set("confirmed", "yes")
	form.Set("shell", "/bin/sh -c command")
	if w := call(h, "POST", "/api/console/open", form.Encode(), "http://reef.test", cookie); w.Code != 400 {
		t.Fatal(w.Code)
	}
	form.Set("shell", "/bin/sh")
	f.rows[0].State = "exited"
	if w := call(h, "POST", "/api/console/open", form.Encode(), "http://reef.test", cookie); w.Code != 409 {
		t.Fatal(w.Code)
	}
	f.rows[0].State = "running"
	s.consoles = map[string]*consoleSession{"one": {owner: "owner", cancel: func() {}}, "two": {owner: "owner", cancel: func() {}}}
	if w := call(h, "POST", "/api/console/open", form.Encode(), "http://reef.test", cookie); w.Code != 429 {
		t.Fatal(w.Code)
	}
	if f.opens != 0 {
		t.Fatal("Unauthorised request opened an exec")
	}
	s.consoles = map[string]*consoleSession{}
	for _, id := range []string{"a", "b", "c", "d"} {
		s.consoles[id] = &consoleSession{owner: "another-session", cancel: func() {}}
	}
	if w := call(h, "POST", "/api/console/open", form.Encode(), "http://reef.test", cookie); w.Code != 429 || f.opens != 0 {
		t.Fatal("Global session limit not enforced", w.Code)
	}
	w := call(h, "GET", "/console?id="+strings.Repeat("a", 64), "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Connect") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "style-src 'self' 'unsafe-inline'") {
		t.Fatal(w.Code)
	}
	w = call(h, "GET", "/", "", "", cookie)
	if strings.Contains(w.Header().Get("Content-Security-Policy"), "unsafe-inline") {
		t.Fatal("Main page CSP changed")
	}
}

func TestConsoleStreamingOwnershipInputResizeAndCleanup(t *testing.T) {
	s, f, cookie := consoleFixture(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	post := func(path string, form url.Values, owner *http.Cookie) *http.Response {
		t.Helper()
		r, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", ts.URL)
		r.AddCookie(owner)
		response, e := (&http.Client{Timeout: 4 * time.Second}).Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return response
	}
	response := post("/api/console/open", consoleForm(), cookie)
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	decoder := json.NewDecoder(response.Body)
	var ready struct{ Type, ID string }
	if e := decoder.Decode(&ready); e != nil || ready.Type != "ready" {
		t.Fatal(ready, e)
	}
	go io.WriteString(f.terminal.w, "terminal output\r\n")
	var output struct{ Type, Data string }
	if e := decoder.Decode(&output); e != nil || output.Type != "output" {
		t.Fatal(output, e)
	}
	b, _ := base64.StdEncoding.DecodeString(output.Data)
	if string(b) != "terminal output\r\n" {
		t.Fatal(string(b))
	}
	form := url.Values{"csrf": {"other"}, "console": {ready.ID}, "data": {base64.StdEncoding.EncodeToString([]byte("echo hi\r"))}}
	other := &http.Cookie{Name: "qcu_session", Value: "other-test"}
	r := post("/api/console/input", form, other)
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatal("Cross-session console access", r.StatusCode)
	}
	r = post("/api/console/close", form, other)
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatal("Cross-session close", r.StatusCode)
	}
	form.Set("csrf", "owner")
	validData := form.Get("data")
	form.Set("data", "invalid-base64!")
	r = post("/api/console/input", form, cookie)
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatal("Invalid input accepted", r.StatusCode)
	}
	form.Set("data", validData)
	r = post("/api/console/input", form, cookie)
	r.Body.Close()
	if r.StatusCode != 204 {
		t.Fatal(r.StatusCode)
	}
	f.terminal.mu.Lock()
	input := f.terminal.input.String()
	f.terminal.mu.Unlock()
	if input != "echo hi\r" {
		t.Fatal(input)
	}
	form.Set("cols", "100")
	form.Set("rows", "40")
	form.Set("cols", "301")
	r = post("/api/console/resize", form, cookie)
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatal("Invalid resize accepted", r.StatusCode)
	}
	form.Set("cols", "100")
	r = post("/api/console/resize", form, cookie)
	r.Body.Close()
	if r.StatusCode != 204 {
		t.Fatal(r.StatusCode)
	}
	f.terminal.mu.Lock()
	cols, rows := f.terminal.cols, f.terminal.rows
	f.terminal.mu.Unlock()
	if cols != 100 || rows != 40 {
		t.Fatal(cols, rows)
	}
	r = post("/api/console/close", form, cookie)
	r.Body.Close()
	if r.StatusCode != 204 {
		t.Fatal(r.StatusCode)
	}
	io.Copy(io.Discard, response.Body)
	select {
	case <-f.terminal.closed:
	case <-time.After(time.Second):
		t.Fatal("Console not closed")
	}
	s.mu.Lock()
	remaining := len(s.consoles)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatal("Console session leaked")
	}
}

func TestConsoleClosesOnClientAbortLogoutAndSessionExpiry(t *testing.T) {
	for _, reason := range []string{"client abort", "logout", "session expiry"} {
		t.Run(reason, func(t *testing.T) {
			s, f, cookie := consoleFixture(t)
			if reason == "session expiry" {
				s.sessions["console-test"] = session{CSRF: "owner", Expires: time.Now().Add(150 * time.Millisecond)}
			}
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()
			r, _ := http.NewRequest("POST", ts.URL+"/api/console/open", strings.NewReader(consoleForm().Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", ts.URL)
			r.AddCookie(cookie)
			response, e := (&http.Client{Timeout: 3 * time.Second}).Do(r)
			if e != nil {
				t.Fatal(e)
			}
			defer response.Body.Close()
			var ready map[string]any
			if e = json.NewDecoder(response.Body).Decode(&ready); e != nil {
				t.Fatal(e)
			}
			switch reason {
			case "client abort":
				response.Body.Close()
			case "logout":
				request, _ := http.NewRequest("POST", ts.URL+"/logout", strings.NewReader("csrf=owner"))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				request.Header.Set("Origin", ts.URL)
				request.AddCookie(cookie)
				loggedOut, e := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
				if e != nil {
					t.Fatal(e)
				}
				loggedOut.Body.Close()
				if loggedOut.StatusCode != 303 {
					t.Fatal(loggedOut.StatusCode)
				}
			}
			select {
			case <-f.terminal.closed:
			case <-time.After(2 * time.Second):
				t.Fatal("Console survived", reason)
			}
		})
	}
}
