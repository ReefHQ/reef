package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reef/internal/agents"
	"reef/internal/docker"
	"strings"
	"sync"
	"testing"
	"time"
)

type statsFixture struct {
	mu                  sync.Mutex
	rows                []docker.Container
	calls, active, peak int
	fail                bool
	gate                chan struct{}
	entered             chan struct{}
}

func (x *statsFixture) List(context.Context) ([]docker.Container, error) {
	return append([]docker.Container{}, x.rows...), nil
}
func (x *statsFixture) ReadStats(ctx context.Context, id string) (docker.Stats, error) {
	x.mu.Lock()
	x.calls++
	x.active++
	if x.active > x.peak {
		x.peak = x.active
	}
	if x.entered != nil {
		x.entered <- struct{}{}
	}
	x.mu.Unlock()
	defer func() { x.mu.Lock(); x.active--; x.mu.Unlock() }()
	if x.gate != nil {
		select {
		case <-x.gate:
		case <-ctx.Done():
			return docker.Stats{}, ctx.Err()
		}
	}
	if x.fail {
		return docker.Stats{}, errors.New("private-stats-error")
	}
	percent := float64(25)
	return docker.Stats{Read: time.Now().UTC(), CPUPercent: &percent}, nil
}
func statsServer(t *testing.T, x *statsFixture) *Server {
	t.Helper()
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{}, "password-long-enough")
	s.discovery = x
	s.sessions[sessionKey("cookie")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	return s
}
func TestStatisticsPaginationFiltersStoppedAndErrorIsolation(t *testing.T) {
	x := &statsFixture{}
	for i := 0; i < 40; i++ {
		state := "running"
		if i%2 == 0 {
			state = "exited"
		}
		x.rows = append(x.rows, docker.Container{ID: fmt.Sprintf("%064x", i+1), Name: fmt.Sprintf("app-%02d", i), State: state, Image: "example:latest", ComposeProject: "project"})
	}
	s := statsServer(t, x)
	cookie := &http.Cookie{Name: "qcu_session", Value: "cookie"}
	w := call(s.Handler(), "GET", "/api/stats", "", "", cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Containers                      []containerStats
		Total, Offset, Limit, HostTotal int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 40 || len(result.Containers) != 32 || result.Limit != 32 || x.calls != 16 {
		t.Fatal(result, x.calls)
	}
	if result.Containers[0].Stats != nil || result.Containers[1].Stats == nil {
		t.Fatal("stopped container sampled", result)
	}
	w = call(s.Handler(), "GET", "/api/stats?offset=32", "", "", cookie)
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Offset != 32 || len(result.Containers) != 8 {
		t.Fatal(result)
	}
	w = call(s.Handler(), "GET", "/api/stats?state=stopped&search=app-00", "", "", cookie)
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Total != 1 || result.Containers[0].Stats != nil {
		t.Fatal(result)
	}
	for _, query := range []string{"offset=-1", "offset=1", "offset=10001", "state=unknown", "search=" + strings.Repeat("x", 513)} {
		if w = call(s.Handler(), "GET", "/api/stats?"+query, "", "", cookie); w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	x.fail = true
	s.stats = newStatsSampler()
	w = call(s.Handler(), "GET", "/api/stats?state=running", "", "", cookie)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-") || !strings.Contains(w.Body.String(), "Statistics unavailable") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(s.Handler(), "GET", "/api/stats", "", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w = call(s.Handler(), "POST", "/api/stats", "csrf=csrf", "http://reef.test", cookie); w.Code != 405 {
		t.Fatal(w.Code)
	}
}
func TestStatisticsSamplerSharesSamplesAndBoundsConcurrency(t *testing.T) {
	x := &statsFixture{gate: make(chan struct{}), entered: make(chan struct{}, 64)}
	sampler := newStatsSampler()
	var jobs sync.WaitGroup
	for i := 0; i < 32; i++ {
		jobs.Add(1)
		go func(i int) { defer jobs.Done(); sampler.read(context.Background(), x, fmt.Sprintf("%064x", i+1)) }(i)
	}
	for i := 0; i < 8; i++ {
		<-x.entered
	}
	// An identical ID joins its existing sample, even while the global slots are full.
	jobs.Add(1)
	go func() { defer jobs.Done(); sampler.read(context.Background(), x, fmt.Sprintf("%064x", 1)) }()
	close(x.gate)
	jobs.Wait()
	x.entered = nil
	if x.peak > 8 || x.calls != 32 {
		t.Fatal(x.peak, x.calls)
	}
	sampler.read(context.Background(), x, fmt.Sprintf("%064x", 1))
	if x.calls != 32 {
		t.Fatal("cache miss", x.calls)
	}
	sampler.mu.Lock()
	sampler.entries[fmt.Sprintf("%064x", 1)].checked = time.Now().Add(-statsTTL)
	sampler.mu.Unlock()
	sampler.read(context.Background(), x, fmt.Sprintf("%064x", 1))
	if x.calls != 33 {
		t.Fatal("expired sample reused", x.calls)
	}
	for i := 0; i < statsCacheSize+10; i++ {
		sampler.read(context.Background(), x, fmt.Sprintf("%064x", i+1000))
	}
	if len(sampler.entries) > statsCacheSize {
		t.Fatal("unbounded cache", len(sampler.entries))
	}
}
func TestStatisticsCancelledSamplesReleaseResourcesAndAreNotCached(t *testing.T) {
	x := &statsFixture{gate: make(chan struct{}), entered: make(chan struct{}, 2)}
	sampler := newStatsSampler()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { _, err := sampler.read(ctx, x, oldContainer); done <- err }()
	<-x.entered
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled sample succeeded")
	}
	if len(sampler.slots) != 0 || len(sampler.entries) != 0 {
		t.Fatal("cancelled sample retained")
	}
	close(x.gate)
	if _, err := sampler.read(context.Background(), x, oldContainer); err != nil {
		t.Fatal(err)
	}
	if x.calls != 2 {
		t.Fatal("cancellation cached")
	}
}
func TestStatisticsRemoteCapabilityAndLocalIsolation(t *testing.T) {
	lx := &statsFixture{rows: []docker.Container{{ID: oldContainer, Name: "local", State: "running"}}}
	central := statsServer(t, lx)
	central.cfg.AgentsEnabled = true
	central.agents, _ = agents.Open(t.TempDir())
	rx := &statsFixture{rows: []docker.Container{{ID: newContainer, Name: "remote", State: "running"}}}
	backend := statsServer(t, rx)
	old, stop := relayAgentFixture(t, central, backend, "legacy", agents.NetworksCapability)
	defer stop()
	host, stopNew := relayAgentFixture(t, central, backend, "capable", agents.StatsCapability)
	defer stopNew()
	cookie := &http.Cookie{Name: "qcu_session", Value: "cookie"}
	h := central.Handler()
	if w := call(h, "GET", "/api/stats?host="+old, "", "", cookie); w.Code != 409 {
		t.Fatal(w.Code)
	}
	w := call(h, "GET", "/api/stats?host="+host, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "remote") || strings.Contains(w.Body.String(), "local") {
		t.Fatal(w.Code, w.Body.String())
	}
	if lx.calls != 0 || rx.calls != 1 {
		t.Fatal("remote read touched Local", lx.calls, rx.calls)
	}
}
