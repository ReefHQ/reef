package agents

import (
	"encoding/json"
	"reef/internal/docker"
	"strings"
	"testing"
	"time"
)

func featureState(d Diagnostic, name string) string {
	for _, f := range d.Features {
		if f.Capability == name {
			return f.State
		}
	}
	return ""
}
func TestDiagnosticFreshnessAndCurrentGenerationCapabilities(t *testing.T) {
	r, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a, key := enrolled(t, r, "Agent A")
	b, bk := enrolled(t, r, "Agent B")
	g, e := r.Begin(a, key, "0.19.0", Protocol)
	if e != nil {
		t.Fatal(e)
	}
	r.Control(a, g, true)
	r.Images(a, g, true)
	d, ok := r.Diagnostic(a, time.Now())
	if !ok || d.Code != "awaiting_inventory" || !d.Host.Stale || d.CapabilitiesCurrent {
		t.Fatal(d)
	}
	if e = r.Publish(a, key, Update{Protocol: Protocol, Generation: g, Sequence: 1, Containers: []docker.Container{}}); e != nil {
		t.Fatal(e)
	}
	d, _ = r.Diagnostic(a, time.Now())
	if d.Code != "healthy" || d.Host.Stale || !d.CapabilitiesCurrent || featureState(d, ImagesCapability) != "reported" || featureState(d, VolumesCapability) != "not_reported" {
		t.Fatal(d)
	}
	now := time.Now()
	stale, _ := r.Diagnostic(a, now.Add(20*time.Second))
	if stale.Code != "inventory_stale" || !stale.Host.Stale {
		t.Fatal(stale)
	}
	offline, _ := r.Diagnostic(a, now.Add(time.Minute))
	if offline.Code != "disconnected" || offline.CapabilitiesCurrent || featureState(offline, ImagesCapability) != "historical" {
		t.Fatal(offline)
	}
	next, e := r.Begin(a, key, "0.20.0", Protocol)
	if e != nil {
		t.Fatal(e)
	}
	r.Communication(a, g, "request_outcome_unknown")
	d, _ = r.Diagnostic(a, time.Now())
	if d.Code != "awaiting_inventory" || !d.Host.Stale || d.CapabilitiesCurrent || len(d.Host.Capabilities) != 0 {
		t.Fatal("old capability/inventory mistaken for new", d)
	}
	for _, o := range d.Observations {
		if o.Code == "request_outcome_unknown" {
			t.Fatal("old request poisoned new generation")
		}
	}
	r.Control(a, next, true)
	r.Publish(a, key, Update{Protocol: Protocol, Generation: next, Sequence: 1, Error: "Local Docker inventory unavailable"})
	d, _ = r.Diagnostic(a, time.Now())
	if d.Code != "docker_unavailable" || d.Docker != "reported_unavailable" || !d.CapabilitiesCurrent {
		t.Fatal(d)
	}
	r.Publish(a, key, Update{Protocol: Protocol, Generation: next, Sequence: 2, Containers: []docker.Container{}})
	d, _ = r.Diagnostic(a, time.Now())
	if d.Code != "healthy" || d.Observations[0].Code != "inventory_recovered" {
		t.Fatal(d)
	}
	bg, _ := r.Begin(b, bk, "0.3.0", Protocol)
	r.Publish(b, bk, Update{Protocol: Protocol, Generation: bg, Sequence: 1, Containers: []docker.Container{}})
	other, _ := r.Diagnostic(b, time.Now())
	if other.Code != "inventory_only" || len(other.Observations) != 2 || other.Host.Name != "Agent B" {
		t.Fatal("host association", other)
	}
}
func TestDiagnosticDoesNotExposePrivateMaterialOrRawAgentText(t *testing.T) {
	root := t.TempDir()
	r, _ := Open(root)
	a, key := enrolled(t, r, "Safe host")
	version := "registry-user:registry-password@private-registry/path"
	g, e := r.Begin(a, key, version, Protocol)
	if e != nil {
		t.Fatal(e)
	}
	r.Control(a, g, true)
	raw := "Bearer " + key + " /run/secrets/identity.json registry-password"
	if e = r.Publish(a, key, Update{Protocol: Protocol, Generation: g, Sequence: 1, Error: raw}); e != nil {
		t.Fatal(e)
	}
	d, _ := r.Diagnostic(a, time.Now())
	data, _ := json.Marshal(d)
	views, _ := json.Marshal(r.Views())
	for _, secret := range []string{key, g, r.data.Hosts[a].CredentialHash, root, "registry-password", "/run/secrets/identity.json", version} {
		if strings.Contains(string(data), secret) || strings.Contains(string(views), secret) {
			t.Fatal("private material exposed", secret)
		}
	}
	if d.Code != "inventory_error" || d.Host.Version != "" {
		t.Fatal(d)
	}
	before := len(d.Observations)
	if _, e = r.Begin(a, "not-the-key", "0.20.0", 99); e == nil {
		t.Fatal("invalid identity accepted")
	}
	after, _ := r.Diagnostic(a, time.Now())
	if len(after.Observations) != before {
		t.Fatal("unauthenticated traffic tainted host")
	}
}
func TestDiagnosticProtocolRejectionRevocationAndRestartScope(t *testing.T) {
	root := t.TempDir()
	r, _ := Open(root)
	a, key := enrolled(t, r, "Agent")
	if _, e := r.Begin(a, key, "0.20.0", 2); e == nil {
		t.Fatal("incompatible protocol accepted")
	}
	d, _ := r.Diagnostic(a, time.Now())
	if d.Code != "protocol_incompatible" || d.NegotiatedProtocol != 0 {
		t.Fatal(d)
	}
	g, _ := r.Begin(a, key, "0.20.0", Protocol)
	r.Control(a, g, true)
	r.Images(a, g, true)
	r.Publish(a, key, Update{Protocol: Protocol, Generation: g, Sequence: 1, Containers: []docker.Container{}})
	if e := r.Change(a, "", true); e != nil {
		t.Fatal(e)
	}
	revoked, _ := r.Diagnostic(a, time.Now())
	if revoked.Code != "revoked" || revoked.Host.Version != "0.20.0" || revoked.Host.LastContact.IsZero() || revoked.CapabilitiesCurrent || featureState(revoked, ImagesCapability) != "historical" {
		t.Fatal(revoked)
	}
	before := len(revoked.Observations)
	r.Communication(a, g, "request_failed")
	revoked, _ = r.Diagnostic(a, time.Now())
	if len(revoked.Observations) != before {
		t.Fatal("revoked observations changed")
	}
	again, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	d, _ = again.Diagnostic(a, time.Now())
	if d.Code != "revoked" || len(d.Observations) != 0 || !d.Host.LastContact.IsZero() {
		t.Fatal("invented restart history", d)
	}
}
func TestDiagnosticObservationBoundsCoalescingAndRejectedInventory(t *testing.T) {
	r, _ := Open("")
	a, key := enrolled(t, r, "Agent")
	g, _ := r.Begin(a, key, "0.20.0", Protocol)
	r.Control(a, g, true)
	r.Communication(a, g, "request_failed")
	r.Communication(a, g, "request_failed")
	d, _ := r.Diagnostic(a, time.Now())
	if d.Observations[0].Count != 2 {
		t.Fatal(d)
	}
	for n := 0; n < MaxDiagnosticEvents+5; n++ {
		code := "request_failed"
		if n%2 == 0 {
			code = "remote_service_error"
		}
		r.Communication(a, g, code)
	}
	d, _ = r.Diagnostic(a, time.Now())
	if len(d.Observations) != MaxDiagnosticEvents {
		t.Fatal("unbounded history")
	}
	if e := r.Publish(a, key, Update{Protocol: Protocol, Generation: g, Sequence: 1, Containers: []docker.Container{{ID: "bad", State: "running"}}}); e == nil {
		t.Fatal("invalid inventory accepted")
	}
	d, _ = r.Diagnostic(a, time.Now())
	if d.Code != "inventory_rejected" || d.LastInventoryAttempt.IsZero() {
		t.Fatal(d)
	}
	before := len(d.Observations)
	r.Communication(a, g, "a bearer secret supplied as event code")
	d, _ = r.Diagnostic(a, time.Now())
	if len(d.Observations) != before {
		t.Fatal("arbitrary observation accepted")
	}
}
