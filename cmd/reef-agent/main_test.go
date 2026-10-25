package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reef/internal/agents"
	"reef/internal/docker"
	"strings"
	"testing"
)

func TestVerifiedHTTPSInventoryEnrollmentAndRevocation(t *testing.T) {
	registry, _ := agents.Open(t.TempDir())
	token, _ := registry.Token("test host")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent/v1/enroll":
			var in struct {
				Token    string
				Protocol int
			}
			json.NewDecoder(r.Body).Decode(&in)
			id, key, e := registry.Enroll(in.Token, in.Protocol)
			if e != nil {
				w.WriteHeader(403)
				return
			}
			json.NewEncoder(w).Encode(identity{ID: id, Credential: key})
		case "/agent/v1/connect":
			g, e := registry.Begin(r.Header.Get("X-Reef-Host"), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "test", agents.Protocol)
			if e != nil {
				w.WriteHeader(403)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"generation": g})
		case "/agent/v1/inventory":
			var in agents.Update
			json.NewDecoder(r.Body).Decode(&in)
			if registry.Publish(r.Header.Get("X-Reef-Host"), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), in) != nil {
				w.WriteHeader(403)
				return
			}
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	c := client{url: server.URL, http: server.Client()}
	ctx := context.Background()
	if e := c.post(ctx, "enroll", map[string]any{"token": token, "protocol": 1}, &c.identity); e != nil {
		t.Fatal(e)
	}
	var connected struct{ Generation string }
	if e := c.post(ctx, "connect", map[string]int{"protocol": 1}, &connected); e != nil {
		t.Fatal(e)
	}
	update := agents.Update{Protocol: 1, Generation: connected.Generation, Sequence: 1, Containers: []docker.Container{{ID: strings.Repeat("a", 64), Name: "TLS fixture", State: "running"}}}
	if e := c.post(ctx, "inventory", update, nil); e != nil {
		t.Fatal(e)
	}
	v, rows, ok := registry.Inventory(c.identity.ID)
	if !ok || v.Stale || len(rows) != 1 {
		t.Fatal(v, rows)
	}
	if e := registry.Change(c.identity.ID, "", true); e != nil {
		t.Fatal(e)
	}
	update.Sequence++
	if e := c.post(ctx, "inventory", update, nil); e == nil {
		t.Fatal("revoked client still published")
	}
	// The normal HTTP client must reject an untrusted TLS issuer.
	untrusted := client{url: server.URL, http: &http.Client{}}
	if e := untrusted.post(ctx, "enroll", map[string]int{"protocol": 1}, nil); e == nil {
		t.Fatal("untrusted certificate accepted")
	}
}
