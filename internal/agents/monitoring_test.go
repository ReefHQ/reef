package agents

import (
	"reef/internal/monitoring"
	"strings"
	"testing"
)

func TestMonitoringNegotiationGenerationFreshnessAndRevocation(t *testing.T) {
	r, _ := Open("")
	id, key := enrolled(t, r, "remote")
	g, _ := r.Begin(id, key, "0.16.0", Protocol)
	r.Control(id, g, true)
	sample := &monitoring.Sample{Sequence: 1, Containers: []monitoring.Container{{ID: strings.Repeat("a", 64), Name: "web", State: "running", Health: "healthy", Restarts: 5}}}
	u := Update{Protocol: Protocol, Generation: g, Sequence: 1, Monitor: sample}
	if r.Publish(id, key, u) == nil {
		t.Fatal("unnegotiated monitoring accepted")
	}
	r.Monitoring(id, g, true)
	if e := r.Publish(id, key, u); e != nil {
		t.Fatal(e)
	}
	value, at, ok := r.MonitorSample(id)
	if !ok || value.Sequence != 1 || at.IsZero() {
		t.Fatal(value, at, ok)
	}
	value.Containers[0].Name = "changed"
	value, _, _ = r.MonitorSample(id)
	if value.Containers[0].Name != "web" {
		t.Fatal("snapshot aliased")
	}
	u.Sequence = 2
	u.Maintenance = true
	r.Publish(id, key, u)
	value, second, _ := r.MonitorSample(id)
	if value.Sequence != 1 || !value.Maintenance || !second.Equal(at) {
		t.Fatal("repeated sample refreshed receipt age")
	}
	g2, _ := r.Begin(id, key, "0.16.0", Protocol)
	if r.CanMonitoring(id) {
		t.Fatal("capability survived reconnect")
	}
	if _, _, ok := r.MonitorSample(id); ok {
		t.Fatal("old sample survived reconnect")
	}
	r.Control(id, g2, true)
	r.Monitoring(id, g2, true)
	u.Generation = g2
	u.Sequence = 1
	r.Publish(id, key, u)
	value, _, _ = r.MonitorSample(id)
	if value.Sequence != 2 {
		t.Fatal("central sequence reset", value)
	}
	r.Change(id, "", true)
	if r.CanMonitoring(id) {
		t.Fatal("revoked monitoring enabled")
	}
	if _, _, ok := r.MonitorSample(id); ok {
		t.Fatal("revoked sample available")
	}
	if r.Publish(id, key, u) == nil {
		t.Fatal("revoked publish accepted")
	}
}

func TestCollectedMaintenanceIsNotClearedByLaterIdleHeartbeat(t *testing.T) {
	r, _ := Open("")
	id, key := enrolled(t, r, "remote")
	g, _ := r.Begin(id, key, "0.16.0", Protocol)
	r.Control(id, g, true)
	r.Monitoring(id, g, true)
	sample := &monitoring.Sample{Sequence: 1, Maintenance: true}
	if err := r.Publish(id, key, Update{Protocol: Protocol, Generation: g, Sequence: 1, Monitor: sample, Maintenance: false}); err != nil {
		t.Fatal(err)
	}
	got, _, _ := r.MonitorSample(id)
	if !got.Maintenance {
		t.Fatal("maintenance snapshot became empty healthy inventory")
	}
	sample = &monitoring.Sample{Sequence: 2}
	r.Publish(id, key, Update{Protocol: Protocol, Generation: g, Sequence: 2, Monitor: sample})
	got, _, _ = r.MonitorSample(id)
	if got.Maintenance {
		t.Fatal("fresh idle snapshot remained muted")
	}
}
