package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reef/internal/agents"
	"reef/internal/app"
	"strings"
	"testing"
	"time"
)

func TestOutboundControlRoundtripAndShutdown(t *testing.T) {
	relay := agents.NewRelay()
	relay.Connect("host", "generation")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Reef-Host") != "host" || r.Header.Get("Authorization") != "Bearer credential" {
			http.Error(w, "auth", 403)
			return
		}
		var input struct {
			Generation string
			Frames     []agents.Frame
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Generation != "generation" {
			http.Error(w, "input", 400)
			return
		}
		if strings.HasSuffix(r.URL.Path, "commands") {
			batch, err := relay.Poll(r.Context(), "host", input.Generation)
			if err != nil {
				return
			}
			json.NewEncoder(w).Encode(batch)
			return
		}
		if err := relay.Results("host", input.Generation, input.Frames); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	backend, err := app.NewAgentBackend("", filepath.Join(t.TempDir(), "managed"), "/missing-docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	peer := &client{url: server.URL, http: server.Client(), identity: identity{ID: "host", Credential: "credential"}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- peer.control(ctx, backend, "generation") }()
	commandCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	response, err := relay.Do(commandCtx, "host", agents.Command{Owner: strings.Repeat("a", 64), Expires: time.Now().Add(time.Hour), Method: "GET", Path: "/api/state"})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || strings.TrimSpace(string(body)) != "[]" || response.StatusCode != 200 {
		cancel()
		t.Fatal(response.StatusCode, string(body), err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("control workers did not drain")
	}
}
