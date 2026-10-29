package agents

import (
	"testing"
)

func TestContainerManagementCapabilityIsGenerationBoundAndRevocable(t *testing.T) {
	r, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	token, _ := r.Token("standalone")
	id, key, _ := r.Enroll(token, Protocol)
	gen, _ := r.Begin(id, key, "0.22.0", Protocol)
	if e = r.Containers(id, gen, true); e != nil || r.CanContainers(id) {
		t.Fatal("Management enabled without control", e)
	}
	r.Control(id, gen, true)
	r.Containers(id, gen, true)
	if !r.CanContainers(id) {
		t.Fatal("Negotiated capability missing")
	}
	newGen, _ := r.Begin(id, key, "0.22.0", Protocol)
	r.Control(id, newGen, true)
	if r.CanContainers(id) {
		t.Fatal("Capability survived generation replacement")
	}
	if e = r.Containers(id, gen, true); e == nil {
		t.Fatal("Old generation changed capability")
	}
	r.Containers(id, newGen, true)
	if !r.CanContainers(id) {
		t.Fatal("Reconnect negotiation failed")
	}
	if e = r.Change(id, "", true); e != nil || r.CanContainers(id) {
		t.Fatal("Revoked agent retained management", e)
	}
}
