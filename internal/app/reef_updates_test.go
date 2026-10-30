package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type reefUpdateTransport func(*http.Request) (*http.Response, error)

func (f reefUpdateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reefTagResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func TestReefVersionOrdering(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		want  int
		valid bool
	}{
		{"v0.10.0", "0.9.99", 1, true}, {"1.0.0", "0.999.99", 1, true}, {"0.25.0", "v0.25.0", 0, true}, {"2.0.0", "10.0.0", -1, true}, {"9999999999999999999999999.0.0", "10.0.0", 1, true}, {"dev", "1.0.0", 0, false}, {"1.0.0-rc.1", "1.0.0", 0, false}, {"01.0.0", "1.0.0", 0, false}, {"1.0.0+meta", "1.0.0", 0, false},
	} {
		if got, valid := compareReefVersions(tc.a, tc.b); got != tc.want || valid != tc.valid {
			t.Fatalf("%+v: %d %v", tc, got, valid)
		}
	}
}
func TestReefUpdateStatesAndCooldown(t *testing.T) {
	for _, tc := range []struct {
		current, want string
		available     bool
	}{{"0.9.0", "available", true}, {"0.25.0", "current", false}, {"1.0.0", "ahead", false}, {"dev", "development", false}, {"0.25.0-rc.1", "development", false}} {
		c := newReefUpdateChecker()
		c.status.Current = tc.current
		calls := 0
		c.client.Transport = reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Host != "api.github.com" || r.URL.Scheme != "https" || r.URL.Path != "/repos/ReefHQ/reef/tags" || r.Header.Get("Authorization") != "" || r.URL.Query().Get("page") != "1" {
				t.Fatal(r)
			}
			return reefTagResponse(200, `[{"name":"v0.9.0"},{"name":"v0.25.0"},{"name":"v9.0.0-rc.1"},{"name":"0.99.0"}]`), nil
		})
		c.check(context.Background())
		got := c.snapshot()
		if got.Status != tc.want || got.Available != tc.available || got.Latest != "0.25.0" || got.Checking || got.CheckedAt.IsZero() {
			t.Fatal(got)
		}
		c.check(context.Background())
		if calls != 1 {
			t.Fatal("Cooldown failed", calls)
		}
		c.attempted = time.Now().Add(-2 * time.Minute)
		c.client.Transport = reefUpdateTransport(func(*http.Request) (*http.Response, error) { return reefTagResponse(403, `rate limited`), nil })
		c.check(context.Background())
		got = c.snapshot()
		if got.Status != "unknown" || got.Available != tc.available || got.Latest != "0.25.0" {
			t.Fatal(got)
		}
	}
}
func TestReefUpdateBoundedPagination(t *testing.T) {
	items := []string{}
	for i := 0; i < 100; i++ {
		items = append(items, fmt.Sprintf(`{"name":"v0.1.%d"}`, i))
	}
	full := "[" + strings.Join(items, ",") + "]"
	for _, bad := range []string{`null`, `{}`, `[{`, strings.Repeat(" ", 2*1024*1024+1), `[]`} {
		c := newReefUpdateChecker()
		c.client.Transport = reefUpdateTransport(func(*http.Request) (*http.Response, error) { return reefTagResponse(200, bad), nil })
		if _, err := c.latest(context.Background()); err == nil {
			t.Fatal("Accepted invalid response")
		}
	}
	c := newReefUpdateChecker()
	calls := 0
	c.client.Transport = reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("page") != fmt.Sprint(calls) {
			t.Fatal(r.URL)
		}
		pageItems := []string{}
		for i := 0; i < 100; i++ {
			pageItems = append(pageItems, fmt.Sprintf(`{"name":"v0.%d.%d"}`, calls, i))
		}
		resp := reefTagResponse(200, "["+strings.Join(pageItems, ",")+"]")
		resp.Header.Set("Link", `<https://evil.invalid>; rel="next"`)
		return resp, nil
	})
	if _, err := c.latest(context.Background()); err == nil || calls != 5 {
		t.Fatal(err, calls)
	}
	calls = 0
	c.client.Transport = reefUpdateTransport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return reefTagResponse(200, full), nil
		}
		return reefTagResponse(200, `[{"name":"v0.12.0"}]`), nil
	})
	if got, err := c.latest(context.Background()); err != nil || got != "v0.12.0" || calls != 2 {
		t.Fatal(got, err, calls)
	}
}
func TestReefUpdateAPIAuthAndCentralRouting(t *testing.T) {
	s, _, admin, csrf := userServer(t)
	calls := 0
	s.reefUpdates.client.Transport = reefUpdateTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return reefTagResponse(200, `[{"name":"v9.0.0"}]`), nil
	})
	h := s.Handler()
	if w := call(h, "GET", "/api/reef-update", "", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	token, _ := s.agents.Token("Remote only")
	remote, _, _ := s.agents.Enroll(token, 1)
	reader := addReader(t, s, "viewer", remote)
	cookie, readerCSRF := userCookie(t, s, reader)
	if w := call(h, "GET", "/api/reef-update?host=remote-only", "", "", cookie); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if calls != 0 {
		t.Fatal("GET caused outbound request")
	}
	if w := call(h, "POST", "/api/reef-update", "csrf="+readerCSRF, "http://reef.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call(h, "POST", "/api/reef-update", "csrf=wrong", "http://reef.test", admin); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call(h, "POST", "/api/reef-update?host=remote-only", "csrf="+csrf, "http://reef.test", admin); w.Code != 200 || calls != 1 {
		t.Fatal(w.Code, calls, w.Body)
	}
}

func TestReefUpdateRedirectCancellationAndConcurrency(t *testing.T) {
	c := newReefUpdateChecker()
	calls := 0
	c.client.Transport = reefUpdateTransport(func(*http.Request) (*http.Response, error) {
		calls++
		resp := reefTagResponse(302, "")
		resp.Header.Set("Location", "https://unexpected.invalid/tags")
		return resp, nil
	})
	if _, err := c.latest(context.Background()); err == nil || calls != 1 {
		t.Fatal("Redirect followed", err, calls)
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c.client.Transport = reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-release:
			return reefTagResponse(200, `[{"name":"v1.0.0"}]`), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { c.check(ctx); close(done) }()
	<-started
	if !c.snapshot().Checking {
		t.Fatal("Missing in-flight state")
	}
	c.check(ctx) // Must return without another outbound request.
	cancel()
	<-done
	if got := c.snapshot(); got.Checking || got.Status != "unknown" {
		t.Fatal(got)
	}
}
