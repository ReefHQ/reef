package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reef/internal/agents"
	"strings"
	"testing"
	"time"
)

func TestAgentDiagnosticsCentralReadOnlyPermissionsAndNoProbe(t *testing.T) {
	s, _, adminCookie, _ := userServer(t)
	token, _ := s.agents.Token("Assigned host")
	host, key, _ := s.agents.Enroll(token, agents.Protocol)
	g, _ := s.agents.Begin(host, key, "0.19.0", agents.Protocol)
	s.agents.Control(host, g, true)
	s.agents.Publish(host, key, agents.Update{Protocol: agents.Protocol, Generation: g, Sequence: 1})
	otherToken, _ := s.agents.Token("Foreign host")
	other, _, _ := s.agents.Enroll(otherToken, agents.Protocol)
	reader := addReader(t, s, "viewer", host)
	cookie, csrf := userCookie(t, s, reader)
	s.relay = nil // Any forwarding/probe would fail this test.
	for _, tc := range []struct {
		method, path, body, origin string
		cookie                     *http.Cookie
		code                       int
	}{
		{"GET", "/api/agent-diagnostics?host=" + host, "", "", nil, 401},
		{"GET", "/api/agent-diagnostics?host=" + host, "", "", cookie, 200},
		{"GET", "/api/agent-diagnostics?host=" + other, "", "", cookie, 403},
		{"POST", "/api/agent-diagnostics?host=" + host, "csrf=" + csrf, "http://reef.test", cookie, 403},
		{"GET", "/api/agent-diagnostics?host=" + strings.Repeat("f", 64), "", "", adminCookie, 404},
	} {
		w := call(s.Handler(), tc.method, tc.path, tc.body, tc.origin, tc.cookie)
		if w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
		if w.Code == 200 {
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("diagnostic cache allowed")
			}
			for _, secret := range []string{key, g, "credential_hash", "generation"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatal("private transport material exposed")
				}
			}
		}
	}
	if agents.AllowedCommand("GET", "/api/agent-diagnostics") || agents.AllowedCommand("POST", "/api/agent-diagnostics") {
		t.Fatal("central diagnostics exposed as agent command")
	}
	if e := s.agents.Change(host, "", true); e != nil {
		t.Fatal(e)
	}
	w := call(s.Handler(), "GET", "/api/agent-diagnostics?host="+host, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"revoked"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

type failingDiagnosticReader struct{}

func (failingDiagnosticReader) Read([]byte) (int, error) {
	return 0, errors.New("private socket/transport secret")
}
func (failingDiagnosticReader) Close() error { return nil }
func TestAgentDiagnosticsCommunicationClassificationsStayGenerationBound(t *testing.T) {
	s, _ := historyServer(t)
	token, _ := s.agents.Token("Remote")
	host, key, _ := s.agents.Enroll(token, agents.Protocol)
	g, _ := s.agents.Begin(host, key, "0.20.0", agents.Protocol)
	s.agents.Control(host, g, true)
	s.agents.Publish(host, key, agents.Update{Protocol: agents.Protocol, Generation: g, Sequence: 1})
	p := &remoteDocker{server: s, host: host, owner: strings.Repeat("a", 64), expires: time.Now().Add(time.Hour)}
	if _, e := p.do(context.Background(), "GET", "/api/state", nil, nil); e == nil {
		t.Fatal("unbound relay unexpectedly worked")
	}
	d, _ := s.agents.Diagnostic(host, time.Now())
	if d.Observations[0].Code != "request_failed" {
		t.Fatal(d)
	}
	before := len(d.Observations)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.diagnosticFailure(ctx, g, "POST", "")
	d, _ = s.agents.Diagnostic(host, time.Now())
	if len(d.Observations) != before {
		t.Fatal("normal cancellation became communication failure")
	}
	p.diagnosticFailure(context.Background(), g, "POST", "")
	d, _ = s.agents.Diagnostic(host, time.Now())
	if d.Observations[0].Code != "request_outcome_unknown" {
		t.Fatal(d)
	}
	body := &diagnosticBody{ReadCloser: failingDiagnosticReader{}, report: func() { p.diagnosticFailure(context.Background(), g, "GET", "") }}
	_, _ = io.ReadAll(body)
	_, _ = body.Read(make([]byte, 1))
	d, _ = s.agents.Diagnostic(host, time.Now())
	if d.Observations[0].Code != "request_failed" || d.Observations[0].Count != 1 {
		t.Fatal("body failure recorded repeatedly", d)
	}
	next, _ := s.agents.Begin(host, key, "0.20.0", agents.Protocol)
	s.agents.Control(host, next, true)
	d, _ = s.agents.Diagnostic(host, time.Now())
	before = len(d.Observations)
	p.diagnosticFailure(context.Background(), g, "POST", "")
	d, _ = s.agents.Diagnostic(host, time.Now())
	if len(d.Observations) != before {
		t.Fatal("stale response tainted new connection")
	}
}
func TestAgentDiagnosticVersionComparisonIsInformational(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		cmp  int
		ok   bool
	}{{"0.19.0", "0.20.0", -1, true}, {"1.2.0", "1.2.0", 0, true}, {"2.0.0", "1.99.0", 1, true}, {"dev", "0.20.0", 0, false}, {"0.20.0", "dev", 0, false}, {"raw-secret", "1.0.0", 0, false}} {
		cmp, ok := compareAgentVersions(tc.a, tc.b)
		if cmp != tc.cmp || ok != tc.ok {
			t.Fatal(tc, cmp, ok)
		}
	}
}
