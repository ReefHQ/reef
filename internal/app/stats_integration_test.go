package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"reef/internal/docker"
	"strings"
	"testing"
	"time"
)

func TestDockerStatisticsIntegration(t *testing.T) {
	if os.Getenv("REEF_TEST_STATS") != "1" {
		t.Skip("opt-in disposable Docker statistics fixture")
	}
	name := "reef-stats-test-" + fmt.Sprint(time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name, "--network", "none", "--entrypoint", "sh", "docker:29.9.2-cli", "-c", "sleep 60").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(raw))
	}
	id := strings.TrimSpace(string(raw))
	defer exec.Command("docker", "rm", "--force", name).Run()
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{}, "password-long-enough")
	s.discovery = docker.NewClient("/var/run/docker.sock")
	s.sessions[sessionKey("cookie")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	w := call(s.Handler(), "GET", "/api/stats?search="+url.QueryEscape(name), "", "", &http.Cookie{Name: "qcu_session", Value: "cookie"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Containers []containerStats
		Total      int
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Containers) != 1 || response.Containers[0].ID != id {
		t.Fatal(w.Body.String())
	}
	sample := response.Containers[0].Stats
	if sample == nil || sample.CPUPercent == nil || sample.CPUInterval <= 0 || sample.MemoryBytes == nil || sample.MemoryLimit == nil {
		t.Fatal(w.Body.String())
	}
	t.Log("Verified bounded non-streaming CPU/memory counters through actual Docker API and authenticated Statistics endpoint; only a disposable container was created")
}
