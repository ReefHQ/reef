package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Explicitly opt in on the development engine. Every write/removal targets a
// uniquely named fixture created by this test, never an existing volume.
func TestDockerVolumesIntegration(t *testing.T) {
	if os.Getenv("REEF_TEST_VOLUMES") != "1" {
		t.Skip("opt-in local Docker volumes fixture")
	}
	name := "reef-volume-test-" + fmt.Sprint(time.Now().UnixNano())
	spare := name + "-spare"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", args...)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v: %v %s", args, err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	defer func() {
		_ = exec.Command("docker", "container", "rm", name).Run()
		for _, volume := range []string{name, spare} {
			_ = exec.Command("docker", "volume", "rm", volume).Run()
		}
	}()
	s := &ImageService{Exec: catalogIntegrationExecutor{t}}
	for _, volume := range []string{name, spare} {
		results := s.runVolumes(ctx, "create", volume, nil)
		if len(results) != 1 || !results[0].OK {
			t.Fatal(results)
		}
	}
	find := func(volume string) CatalogVolume {
		t.Helper()
		rows, err := s.volumes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Name == volume {
				return row
			}
		}
		t.Fatal("fixture missing")
		return CatalogVolume{}
	}
	row := find(name)
	if !row.Removable || row.Created == "" || row.Driver != "local" {
		t.Fatal(row)
	}
	docker("run", "--rm", "--network", "none", "--mount", "type=volume,source="+name+",target=/fixture", "--entrypoint", "sh", "docker:29.9.2-cli", "-c", "printf reef-volume-marker > /fixture/marker")
	docker("create", "--name", name, "--network", "none", "--mount", "type=volume,source="+name+",target=/fixture", "--entrypoint", "sh", "docker:29.9.2-cli", "-c", "true")
	used := find(name)
	if used.Removable || len(used.Containers) != 1 {
		t.Fatal(used)
	}
	targets := []VolumeTarget{{name, used.Fingerprint}, {spare, find(spare).Fingerprint}}
	results := s.runVolumes(ctx, "cleanup", "", targets)
	if results[0].OK || !results[1].OK {
		t.Fatal(results)
	}
	if marker := docker("run", "--rm", "--network", "none", "--mount", "type=volume,source="+name+",target=/fixture,readonly", "--entrypoint", "cat", "docker:29.9.2-cli", "/fixture/marker"); marker != "reef-volume-marker" {
		t.Fatal("data changed")
	}
	docker("container", "rm", name)
	old := find(name)
	docker("volume", "rm", name)
	docker("volume", "create", "--label", "io.reef.recreated="+name, name)
	if err := s.removeVolume(ctx, VolumeTarget{name, old.Fingerprint}); err == nil {
		t.Fatal("recreated volume removed with stale fingerprint")
	}
	fresh := find(name)
	if err := s.removeVolume(ctx, VolumeTarget{name, fresh.Fingerprint}); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("docker", "volume", "inspect", name).Run(); err == nil {
		t.Fatal("fixture volume remains")
	}
	t.Log("Verified: local volume creation/metadata, stopped-container protection with preserved marker, targeted mixed batch, recreated-name refusal and final non-force removal")
}
