package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reef/internal/agents"
	"reef/internal/docker"
	"reef/internal/history"
	"reef/internal/monitoring"
	"reef/internal/notifications"
	"strings"
	"testing"
)

func TestMonitoringSettingsCentralAuthenticationValidationAndNoDockerMutation(t *testing.T) {
	s, _ := historyServer(t)
	s.monitor, _ = monitoring.Open(t.TempDir())
	cookie := &http.Cookie{Name: "qcu_session", Value: "cookie"}
	h := s.Handler()
	body := "csrf=csrf&confirmed=yes&enabled=true&disconnect_seconds=120&unhealthy_seconds=120&restart_count=3&restart_window_seconds=300&recovery_seconds=60"
	for _, tc := range []struct {
		method, path, body, origin string
		cookie                     *http.Cookie
		code                       int
	}{
		{"GET", "/api/monitoring", "", "", nil, 401}, {"POST", "/api/monitoring", body, "http://evil.test", cookie, 403}, {"POST", "/api/monitoring", strings.Replace(body, "csrf=csrf", "csrf=wrong", 1), "http://reef.test", cookie, 403}, {"POST", "/api/monitoring", strings.Replace(body, "restart_count=3", "restart_count=1", 1), "http://reef.test", cookie, 400}, {"POST", "/api/monitoring?host=" + strings.Repeat("a", 64), body, "http://reef.test", cookie, 200}, {"GET", "/api/monitoring?host=all", "", "", cookie, 200},
	} {
		if w := call(h, tc.method, tc.path, tc.body, tc.origin, tc.cookie); w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	if !s.monitor.Enabled() {
		t.Fatal("policy not enabled")
	}
	if agents.AllowedCommand("GET", "/api/monitoring") || agents.AllowedCommand("POST", "/api/monitoring") {
		t.Fatal("central policy exposed as agent command")
	}
}
func TestMonitoringEventOutboxIndependentSinksHostBindingAndDedup(t *testing.T) {
	s, _ := historyServer(t)
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "monitoring"), 0700)
	e := notifications.New("monitor", "unhealthy", "failed", "", "database")
	e.Operation = e.ID
	e.HostID = strings.Repeat("a", 64)
	e.Host = "remote"
	raw, _ := json.Marshal(map[string]any{"version": 1, "config": monitoring.Defaults(), "incidents": []any{}, "pending": []notifications.Event{e}, "holds": map[string]any{}})
	os.WriteFile(filepath.Join(dir, "monitoring", "state.json"), raw, 0600)
	var err error
	s.monitor, err = monitoring.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	working := s.webhooks
	s.webhooks = notifications.Unavailable(false)
	s.dispatchMonitoring()
	if len(s.monitor.Pending()) != 1 {
		t.Fatal("failed sink acknowledged")
	}
	page, _ := s.history.Query(history.Query{Host: e.HostID})
	if page.Total != 1 || page.Rows[0].Note != "Monitoring incident opened" {
		t.Fatal(page)
	}
	s.webhooks = working
	s.dispatchMonitoring()
	s.dispatchMonitoring()
	page, _ = s.history.Query(history.Query{Host: e.HostID})
	if len(s.monitor.Pending()) != 0 || page.Total != 1 {
		t.Fatal(page)
	}
	if err = s.history.Accept(e.HostID, "remote", []notifications.Event{e}); err == nil {
		t.Fatal("remote forged monitoring event")
	}
	if err = s.webhooks.Accept(e.HostID, "remote", []notifications.Event{e}); err == nil {
		t.Fatal("forged webhook monitor event")
	}
}

type monitorDiscovery struct {
	inventoryStub
	busy *Server
}

func (m *monitorDiscovery) ReadMonitor(ctx context.Context, id string) (monitoring.Container, error) {
	if m.busy != nil {
		m.busy.m.mu.Lock()
		m.busy.m.storageBusy = true
		m.busy.m.mu.Unlock()
	}
	return monitoring.Container{ID: id, State: "running", Health: "unhealthy", Restarts: 5}, nil
}
func TestMonitoringCollectionHostMaintenanceBeforeAndAfterRead(t *testing.T) {
	s, _ := historyServer(t)
	s.discovery = &monitorDiscovery{inventoryStub: inventoryStub{rows: []docker.Container{{ID: strings.Repeat("a", 64), Name: "web", State: "running"}}}}
	sample := s.collectMonitoring(context.Background())
	if sample.Error != "" || len(sample.Containers) != 1 || sample.Maintenance {
		t.Fatal(sample)
	}
	s.m.storageBusy = true
	if sample = s.collectMonitoring(context.Background()); !sample.Maintenance || len(sample.Containers) != 0 {
		t.Fatal(sample)
	}
	s.m.storageBusy = false
	s.discovery.(*monitorDiscovery).busy = s
	if sample = s.collectMonitoring(context.Background()); !sample.Maintenance {
		t.Fatal("mutation during collection not suppressed")
	}
}
func TestMonitoringProtocolToggleWithoutReconnectAndLegacyInventory(t *testing.T) {
	s, _ := historyServer(t)
	s.monitor, _ = monitoring.Open(t.TempDir())
	token, _ := s.agents.Token("remote")
	id, key, _ := s.agents.Enroll(token, agents.Protocol)
	machine := func(path string, input any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(input)
		r := httptest.NewRequest("POST", path, bytes.NewReader(b))
		r.Header.Set("X-Reef-Host", id)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	connected := machine("/agent/v1/connect", map[string]any{"protocol": 1, "version": "0.16.0", "capabilities": []string{agents.ControlCapability, agents.MonitoringCapability}})
	if connected.Code != 200 {
		t.Fatal(connected.Body.String())
	}
	var response struct {
		Generation string
		Monitoring bool
	}
	json.Unmarshal(connected.Body.Bytes(), &response)
	if !response.Monitoring {
		t.Fatal("missing negotiation")
	}
	u := agents.Update{Protocol: 1, Generation: response.Generation, Sequence: 1}
	w := machine("/agent/v1/inventory", u)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"monitoring_enabled":false`) {
		t.Fatal(w.Body.String())
	}
	c := monitoring.Defaults()
	c.Enabled = true
	s.monitor.Configure(c)
	u.Sequence++
	w = machine("/agent/v1/inventory", u)
	if !strings.Contains(w.Body.String(), `"monitoring_enabled":true`) {
		t.Fatal(w.Body.String())
	}
	// A reconnect without the capability keeps the legacy 204 contract.
	connected = machine("/agent/v1/connect", map[string]any{"protocol": 1, "version": "0.15.0", "capabilities": []string{agents.ControlCapability}})
	json.Unmarshal(connected.Body.Bytes(), &response)
	u.Generation = response.Generation
	u.Sequence = 1
	w = machine("/agent/v1/inventory", u)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestCachedRemoteObservationBeforePolicyChangeIsNotReused(t *testing.T) {
	s, _ := historyServer(t)
	s.monitor, _ = monitoring.Open(t.TempDir())
	c := monitoring.Defaults()
	c.Enabled = true
	s.monitor.Configure(c)
	token, _ := s.agents.Token("remote")
	id, key, _ := s.agents.Enroll(token, 1)
	g, _ := s.agents.Begin(id, key, "0.16.0", 1)
	s.agents.Control(id, g, true)
	s.agents.Monitoring(id, g, true)
	s.agents.Publish(id, key, agents.Update{Protocol: 1, Generation: g, Sequence: 1, Monitor: &monitoring.Sample{Sequence: 1, Containers: []monitoring.Container{{ID: strings.Repeat("a", 64), Name: "web", State: "running", Health: "unhealthy"}}}})
	c.UnhealthySeconds = 60
	s.monitor.Configure(c)
	s.observeMonitoringHosts()
	v := s.monitor.View()
	if len(v.Hosts) != 1 || !v.Hosts[0].Checked.IsZero() {
		t.Fatal("old sample established a new policy baseline", v)
	}
}
