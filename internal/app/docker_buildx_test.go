package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerExecutorBuildxMetadataIsPrivateAndRemoved(t *testing.T) {
	bin := t.TempDir()
	temp := t.TempDir()
	home := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", temp)
	t.Setenv("BUILDX_CONFIG", "/must-not-inherit")
	t.Setenv("BUILDX_BUILDER", "must-not-inherit")
	t.Setenv("DOCKER_CONFIG", "/must-not-inherit")
	t.Setenv("QCU_PASSWORD", "private-fixture-only")
	config := filepath.Join(home, ".docker")
	if e := os.Mkdir(config, 0700); e != nil {
		t.Fatal(e)
	}
	config = filepath.Join(config, "config.json")
	before := []byte(`{"auths":{},"fixture":"unchanged"}`)
	if e := os.WriteFile(config, before, 0400); e != nil {
		t.Fatal(e)
	}
	script := `#!/bin/sh
set -eu
test -z "${BUILDX_BUILDER+x}"
test -z "${DOCKER_CONFIG+x}"
test -z "${QCU_PASSWORD+x}"
printf '%s\n' "$HOME" "$DOCKER_HOST"
if [ "$1" = buildx ] || [ "$1" = compose ]; then
 test -d "$BUILDX_CONFIG"
 printf '%s\n' "$BUILDX_CONFIG"
 ls -ld "$BUILDX_CONFIG"
 printf metadata > "$BUILDX_CONFIG/state"
 if [ "$2" = fail ]; then exit 7; fi
else
 test -z "${BUILDX_CONFIG+x}"
fi
`
	if e := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for _, args := range [][]string{{"buildx", "du"}, {"compose", "build"}, {"compose", "fail"}, {"image", "inspect"}} {
		var out bytes.Buffer
		e := (DockerExecutor{Socket: "/fixture/engine.sock"}).Output(context.Background(), Project{}, args, &out)
		if (e != nil) != (args[1] == "fail") {
			t.Fatal("Unexpected command result", args, e)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if lines[0] != home || lines[1] != "unix:///fixture/engine.sock" {
			t.Fatal("Docker auth location or endpoint changed")
		}
		if args[0] != "image" {
			dir := lines[2]
			rel, err := filepath.Rel(temp, dir)
			if err != nil || strings.HasPrefix(rel, "..") || seen[dir] || !strings.HasPrefix(lines[3], "drwx------") {
				t.Fatal("Metadata is not private and isolated")
			}
			seen[dir] = true
			if _, err = os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("Temporary metadata retained after command", err)
			}
		}
	}
	after, e := os.ReadFile(config)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("Docker credentials/config were modified", e)
	}
	entries, e := os.ReadDir(temp)
	if e != nil || len(entries) != 0 {
		t.Fatal("Temporary metadata leaked", e)
	}
}

func TestDockerExecutorBuildxMetadataFailureIsSanitized(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "private-missing-parent"))
	e := (DockerExecutor{}).Output(context.Background(), Project{}, []string{"buildx", "du"}, io.Discard)
	if e == nil || e.Error() != "Cannot prepare private Buildx metadata in temporary storage" {
		t.Fatal("Unexpected temporary-state failure", e)
	}
}

// Run the compiled test binary in a disposable production-like read-only image,
// with only /tmp writable and synthetic Docker credentials mounted read-only.
// This test performs metadata/config reads only; no build, pull or prune.
func TestDockerReadOnlyBuildxIntegration(t *testing.T) {
	if os.Getenv("REEF_TEST_READONLY_BUILDX") != "1" {
		t.Skip("opt-in read-only Docker CLI integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config := filepath.Join(os.Getenv("HOME"), ".docker", "config.json")
	before, e := os.ReadFile(config)
	if e != nil {
		t.Fatal("Synthetic Docker config missing")
	}
	s := &ImageService{Exec: DockerExecutor{}}
	records, e := s.cacheRecords(ctx, "")
	if e != nil {
		t.Fatal("Read-only cache inspection failed", e)
	}
	for _, r := range records {
		if !buildCacheIDPattern.MatchString(r.ID) || r.Size < 0 {
			t.Fatal("Invalid cache metadata")
		}
	}
	root := t.TempDir()
	if e = os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services:\n  app:\n    build: .\n"), 0600); e != nil {
		t.Fatal(e)
	}
	p := Project{Directory: root, ProjectName: "reef-readonly-config-fixture", ComposeFiles: []string{"compose.yaml"}}
	rev, e := revision(p)
	if e != nil {
		t.Fatal(e)
	}
	m := &Manager{Images: s}
	services, e := m.buildServices(ctx, p, rev)
	if e != nil || len(services) != 1 || services[0] != "app" {
		t.Fatal("Read-only Compose inspection failed", e)
	}
	after, e := os.ReadFile(config)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("Read-only Docker config changed", e)
	}
	matches, e := filepath.Glob(filepath.Join(os.TempDir(), "reef-buildx-*"))
	if e != nil || len(matches) != 0 {
		t.Fatal("Metadata remained after read-only commands", e)
	}
	t.Logf("Read %d cache records and saved Compose build metadata with read-only root/config; no Docker mutation", len(records))
}
