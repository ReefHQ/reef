package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Optional integration check: parse fixtures with an installed Compose CLI.
// Never addresses a real daemon: DOCKER_HOST points to a nonexistent test socket.
func TestRealComposeConfigWithoutEngine(t *testing.T) {
	if os.Getenv("QCU_TEST_DOCKER_CONFIG") != "1" {
		t.Skip("optional: QCU_TEST_DOCKER_CONFIG=1, configuration validation only")
	}
	docker, e := exec.LookPath("docker")
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	for f, b := range map[string]string{
		"compose.yaml":  "services:\n  web:\n    image: nginx:${TAG}\n    env_file: ./app.env\n    build: ./build\n    volumes:\n      - ./data:/data\n",
		"override.yaml": "services:\n  web:\n    environment:\n      EXTRA: override-ok\n",
		".env":          "TAG=alpine\n", "app.env": "APP_MODE=fixture\n", "build/Dockerfile": "FROM scratch\n", "data/keep": "precious",
	} {
		path := filepath.Join(root, f)
		os.MkdirAll(filepath.Dir(path), 0700)
		if e = os.WriteFile(path, []byte(b), 0600); e != nil {
			t.Fatal(e)
		}
	}
	b, _ := os.ReadFile(filepath.Join(root, "compose.yaml"))
	f, e := os.CreateTemp(root, ".qcu-candidate-*.yaml")
	if e != nil {
		t.Fatal(e)
	}
	candidate := f.Name()
	defer os.Remove(candidate)
	if _, e = f.Write(b); e != nil {
		t.Fatal(e)
	}
	f.Close()
	p := Project{Directory: root, ComposeFiles: []string{"compose.yaml", "override.yaml"}, ProjectName: "existing-fixture"}
	parse := func(candidate string) map[string]any {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, docker, composeArgs(p, candidate, "config", "--format", "json")...)
		cmd.Dir = root
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "DOCKER_HOST=unix://" + filepath.Join(root, "NO_ENGINE.sock"), "COMPOSE_ANSI=never"}
		out, e := cmd.Output()
		if e != nil {
			if ee, ok := e.(*exec.ExitError); ok {
				t.Fatalf("configuration without engine: %s: %s", e, ee.Stderr)
			}
			t.Fatal(e)
		}
		var obj map[string]any
		if e = json.Unmarshal(out, &obj); e != nil {
			t.Fatal(e)
		}
		return obj
	}
	original := parse("")
	proposed := parse(candidate)
	for _, obj := range []map[string]any{original, proposed} {
		if obj["name"] != "existing-fixture" {
			t.Fatal("project identity lost")
		}
		web := obj["services"].(map[string]any)["web"].(map[string]any)
		if web["image"] != "nginx:alpine" {
			t.Fatal(".env not resolved", web["image"])
		}
		env := web["environment"].(map[string]any)
		if env["APP_MODE"] != "fixture" || env["EXTRA"] != "override-ok" {
			t.Fatal("env_file or override lost", env)
		}
		build := web["build"].(map[string]any)
		if build["context"] != filepath.Join(root, "build") {
			t.Fatal("build context moved", build)
		}
		volume := web["volumes"].([]any)[0].(map[string]any)
		if volume["source"] != filepath.Join(root, "data") {
			t.Fatal("bind mount moved", volume)
		}
	}
	// Compose records source filenames in some metadata; compare actual service definitions.
	a, _ := json.Marshal(original["services"])
	b, _ = json.Marshal(proposed["services"])
	if string(a) != string(b) {
		t.Fatal("candidate changes the service model")
	}
	data, _ := os.ReadFile(filepath.Join(root, "data/keep"))
	if string(data) != "precious" {
		t.Fatal("data modified")
	}
}
