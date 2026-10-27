package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in only, using a dedicated writable host fixture folder mounted at the
// identical path in the test container. Never run against a production engine.
func TestDockerImageCleanupIntegration(t *testing.T) {
	root := os.Getenv("REEF_TEST_IMAGE_ROOT")
	if root == "" {
		t.Skip("opt-in local Docker fixture")
	}
	if !filepath.IsAbs(root) || !strings.Contains(root, "/reef/work/image-updates/") {
		t.Fatal("fixture must be under Reef work/image-updates")
	}
	name := "reef-image-cleanup-test-" + fmt.Sprint(time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	docker := func(input string, args ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, "docker", args...)
		command.Env = append(os.Environ(), "DOCKER_HOST=unix:///var/run/docker.sock")
		command.Stdin = strings.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v: %v %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	oldTag, newTag := name+":old", name+":new"
	imagesToClean := []string{oldTag, newTag}
	defer func() {
		for _, container := range []string{name + "-shared", name + "-current"} {
			_ = exec.Command("docker", "rm", "-f", container).Run()
		}
		for _, image := range imagesToClean {
			_ = exec.Command("docker", "image", "rm", image).Run()
		}
	}()
	for _, tag := range []string{oldTag, newTag} {
		docker("FROM docker:29.9.2-cli\nLABEL io.reef.test="+tag+"\n", "build", "--pull=false", "-t", tag, "-")
	}
	oldID := docker("", "image", "inspect", "--format", "{{.Id}}", oldTag)
	newID := docker("", "image", "inspect", "--format", "{{.Id}}", newTag)
	imagesToClean = append(imagesToClean, oldID, newID)
	file := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(file, []byte("version: '3.8'\nservices:\n  web:\n    image: "+newTag+"\n    build: .\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := Project{Directory: root, ProjectName: name, ComposeFiles: []string{"compose.yaml"}}
	docker("", "run", "-d", "--name", name+"-current", "--network", "none", "--label", "com.docker.compose.project="+name, "--label", "com.docker.compose.service=web", "--label", "com.docker.compose.oneoff=False", "--label", "com.docker.compose.config-hash=fixture", "--label", "com.docker.compose.container-number=1", "--entrypoint", "sh", newTag, "-c", "sleep 300")
	docker("", "create", "--name", name+"-shared", "--entrypoint", "sh", oldTag, "-c", "true")
	s := &ImageService{Exec: DockerExecutor{}}
	if report, err := s.check(ctx, p); err != nil || len(report) != 1 || report[0].Status != "local" {
		t.Fatalf("Compose diagnostics corrupted structured output: %+v %v", report, err)
	}
	if rows, err := s.containers(ctx, p); err != nil || len(rows) != 1 {
		t.Fatalf("Cannot inspect fixture containers: %v %v; raw Compose: %s; raw inspect: %s", rows, err, docker("", composeArgs(p, "", "ps", "--all", "--quiet")...), docker("", "container", "inspect", "--format", containerImageFormat, name+"-current"))
	}
	before := []containerImage{{Service: "web", ImageID: oldID}}
	var log bytes.Buffer
	// A stopped container elsewhere keeps the old image even after its tag moves.
	docker("", "tag", newTag, oldTag)
	s.cleanup(ctx, p, before, &log)
	if !strings.Contains(log.String(), "still be used") {
		t.Fatal("shared image not protected", log.String())
	}
	docker("", "image", "inspect", oldID)
	docker("", "rm", name+"-shared")
	log.Reset()
	s.cleanup(ctx, p, before, &log)
	if !strings.Contains(log.String(), "Removed replaced unused image "+oldID) {
		t.Fatal("old image not removed", log.String())
	}
	if err := exec.Command("docker", "image", "inspect", oldID).Run(); err == nil {
		t.Fatal("old image still exists")
	}
	docker("", "image", "inspect", newID)
	t.Log("Verified: shared stopped-container image retained, then removed when unused; current image preserved.")
}
