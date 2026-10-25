package app

import (
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

type multiConsoleStub struct {
	inventoryStub
	mu        sync.Mutex
	terminals []*fakeConsole
}

func (f *multiConsoleStub) OpenConsole(context.Context, string, string, int, int) (docker.Console, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	terminal := newFakeConsole()
	f.terminals = append(f.terminals, terminal)
	return terminal, nil
}

func TestSharedConsoleTransportSupportsEightSessionsAndCleansThemUp(t *testing.T) {
	s, _, cookie := consoleFixture(t)
	f := &multiConsoleStub{inventoryStub: inventoryStub{rows: []docker.Container{{ID: strings.Repeat("a", 64), Name: "fixture", State: "running"}}}}
	s.discovery = f
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	post := func(path string, form url.Values, owner *http.Cookie) *http.Response {
		t.Helper()
		r, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", ts.URL)
		r.AddCookie(owner)
		res, e := (&http.Client{Timeout: 5 * time.Second}).Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return res
	}
	stream := post("/api/console/stream", url.Values{"csrf": {"owner"}}, cookie)
	defer stream.Body.Close()
	decoder := json.NewDecoder(stream.Body)
	var transport struct{ Type, ID string }
	if e := decoder.Decode(&transport); e != nil || transport.Type != "transport" {
		t.Fatal(transport, e)
	}
	ids := map[string]bool{}
	for i := 0; i < 8; i++ {
		form := consoleForm()
		form.Set("transport", transport.ID)
		form.Set("client", string(rune('a'+i)))
		res := post("/api/console/open", form, cookie)
		if res.StatusCode != 200 {
			b, _ := io.ReadAll(res.Body)
			t.Fatal(res.StatusCode, string(b))
		}
		res.Body.Close()
	}
	for i := 0; i < 8; i++ {
		var frame struct{ Type, Client, Console string }
		if e := decoder.Decode(&frame); e != nil || frame.Type != "ready" {
			t.Fatal(frame, e)
		}
		ids[frame.Console] = true
	}
	if len(ids) != 8 {
		t.Fatal("Sessions were not independent", len(ids))
	}
	form := consoleForm()
	form.Set("transport", transport.ID)
	form.Set("client", "ninth")
	res := post("/api/console/open", form, cookie)
	res.Body.Close()
	if res.StatusCode != 429 {
		t.Fatal("Default per-login limit", res.StatusCode)
	}
	form.Set("csrf", "other")
	res = post("/api/console/open", form, &http.Cookie{Name: "qcu_session", Value: "other-test"})
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("Cross-session transport access", res.StatusCode)
	}
	f.mu.Lock()
	terminals := append([]*fakeConsole(nil), f.terminals...)
	f.mu.Unlock()
	go io.WriteString(terminals[0].w, "first shell")
	var output struct{ Type, Client, Data string }
	if e := decoder.Decode(&output); e != nil || output.Type != "output" || output.Client != "a" {
		t.Fatal(output, e)
	}
	b, _ := base64.StdEncoding.DecodeString(output.Data)
	if string(b) != "first shell" {
		t.Fatal(string(b))
	}
	// Ordinary requests remain independent of the single persistent output stream.
	r, _ := http.NewRequest("GET", ts.URL+"/api/state", nil)
	r.AddCookie(cookie)
	ordinary, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	ordinary.Body.Close()
	if ordinary.StatusCode != 200 {
		t.Fatal(ordinary.StatusCode)
	}
	stream.Body.Close()
	for _, terminal := range terminals {
		select {
		case <-terminal.closed:
		case <-time.After(2 * time.Second):
			t.Fatal("Transport closure leaked a shell")
		}
	}
}

func TestConsoleLimitConfiguration(t *testing.T) {
	if per, total := (Config{}).consoleLimits(); per != 8 || total != 16 {
		t.Fatal(per, total)
	}
	for _, c := range []Config{{}, {ConsoleSessionsPerLogin: 12, ConsoleSessionsTotal: 24}, {ConsoleSessionsPerLogin: 1, ConsoleSessionsTotal: 1}} {
		if e := validateConsoleLimits(c); e != nil {
			t.Fatal(e)
		}
	}
	for _, c := range []Config{{ConsoleSessionsPerLogin: -1}, {ConsoleSessionsTotal: -1}, {ConsoleSessionsPerLogin: 65, ConsoleSessionsTotal: 100}, {ConsoleSessionsTotal: 129}, {ConsoleSessionsPerLogin: 20, ConsoleSessionsTotal: 10}} {
		if e := validateConsoleLimits(c); e == nil {
			t.Fatal("Invalid console limit accepted", c)
		}
	}
}

func TestEmbeddedConsoleKeepsFramingRestrictedToSameOrigin(t *testing.T) {
	s, _, cookie := consoleFixture(t)
	h := s.Handler()
	w := call(h, "GET", "/console?id="+strings.Repeat("a", 64)+"&embedded=1&channel=console-1", "", "", cookie)
	if w.Code != 200 || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") || !strings.Contains(w.Body.String(), "/assets/console-frame.js") {
		t.Fatal(w.Code, w.Header())
	}
	w = call(h, "GET", "/", "", "", cookie)
	if w.Header().Get("X-Frame-Options") != "DENY" || strings.Contains(w.Header().Get("Content-Security-Policy"), "unsafe-inline") {
		t.Fatal("Main UI protections changed")
	}
}
