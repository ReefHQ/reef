package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestInventoryIncludesStoppedAndOnlyWhitelistedMetadata(t *testing.T) {
	c := &Client{http: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/containers/json" || r.URL.Query().Get("all") != "1" {
			t.Fatal(r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[
 {"Id":"1","Names":["/z-running"],"Image":"app:latest","State":"running","Status":"Up 2 hours","Command":"private-command","Labels":{"com.docker.compose.project":"real-project","com.docker.compose.service":"web","com.docker.compose.project.working_dir":"/untrusted/project","com.docker.compose.project.config_files":"/untrusted/project/compose.yaml,/untrusted/project/override.yaml","io.reef.local":"own-installation","secret":"private-label"},"Env":["PASSWORD=private-env"]},
 {"Id":"2","Names":["/a-stopped"],"Image":"worker:old","State":"exited","Status":"Exited (0) 1 day ago","Labels":{}}
 ]`))}, nil
	})}}
	rows, e := c.List(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 2 || rows[0].Name != "a-stopped" || rows[0].State != "exited" || rows[1].ComposeProject != "real-project" || rows[1].ComposeService != "web" {
		t.Fatal(rows)
	}
	if rows[1].WorkingDir != "/untrusted/project" || rows[1].ConfigFiles != "/untrusted/project/compose.yaml,/untrusted/project/override.yaml" || !rows[1].Reef {
		t.Fatal("Compose hints not retained")
	}
	if hint(strings.Repeat("x", 8193)) != "" {
		t.Fatal("unbounded label")
	}
	b, _ := json.Marshal(rows)
	for _, private := range []string{"private-command", "private-label", "private-env", "PASSWORD", "Labels", "Env", "/untrusted", "own-installation"} {
		if strings.Contains(string(b), private) {
			t.Fatalf("unexpected metadata disclosure: %s", private)
		}
	}
}
func TestInventoryErrorsAreUsefulAndDoNotLeakResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		failure    bool
	}{
		{"daemon error", "private-server-error", 503, false}, {"invalid JSON", "private-invalid-json", 200, false}, {"transport", "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{http: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				if tc.failure {
					return nil, errors.New("private-transport-error")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			_, e := c.List(context.Background())
			if e == nil || strings.Contains(e.Error(), "private-") {
				t.Fatal(e)
			}
		})
	}
}
func TestEmptyInventoryReturnsArray(t *testing.T) {
	c := &Client{http: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[]`))}, nil
	})}}
	rows, e := c.List(context.Background())
	if e != nil || rows == nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
}
