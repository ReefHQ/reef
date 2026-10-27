package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Opt-in on the development Docker engine only. Creates unique scratch images
// with no RUN steps and a never-started container; deletes only these fixtures.
func TestDockerImageCatalogIntegration(t *testing.T) {
	if os.Getenv("REEF_TEST_IMAGE_CATALOG") != "1" {
		t.Skip("opt-in local Docker fixture")
	}
	name := "reef-catalog-test-" + fmt.Sprint(time.Now().UnixNano())
	one, two, three := name+":one", name+":two", name+":three"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docker := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stdin = strings.NewReader(input)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v: %v %s", args, err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	defer func() {
		_ = exec.Command("docker", "container", "rm", name).Run()
		for _, tag := range []string{one, two, three} {
			_ = exec.Command("docker", "image", "rm", tag).Run()
		}
	}()
	docker("FROM scratch\nLABEL io.reef.catalog-test="+name+"\n", "build", "--pull=false", "-t", one, "-")
	id := docker("", "image", "inspect", "--format", "{{.Id}}", one)
	s := &ImageService{Exec: catalogIntegrationExecutor{t}}
	target := func() ImageTarget {
		t.Helper()
		rows, err := s.catalog(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID == id {
				return ImageTarget{row.ID, row.Fingerprint}
			}
		}
		t.Fatal("fixture missing")
		return ImageTarget{}
	}
	if err := s.mutateCatalogImage(ctx, "tag", two, target()); err != nil {
		t.Fatal(err)
	}
	docker("", "create", "--name", name, "--entrypoint", "/never-executed", one)
	if err := s.mutateCatalogImage(ctx, "remove", "", target()); err == nil {
		t.Fatal("image used by stopped container removed")
	}
	docker("", "container", "rm", name)
	stale := target()
	if err := s.mutateCatalogImage(ctx, "tag", three, target()); err != nil {
		t.Fatal(err)
	}
	if err := s.mutateCatalogImage(ctx, "remove", "", stale); err == nil {
		t.Fatal("changed tags accepted")
	}
	if err := s.mutateCatalogImage(ctx, "untag", three, target()); err != nil {
		t.Fatal(err)
	}
	if err := s.mutateCatalogImage(ctx, "remove", "", target()); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("docker", "image", "inspect", id).Run(); err == nil {
		t.Fatal("fixture image still exists")
	}
	t.Log("Verified real image metadata, add/remove tags, stopped-container protection, changed-tag rejection and multi-tag deletion without force/prune")
}

type catalogIntegrationExecutor struct{ t *testing.T }

func (x catalogIntegrationExecutor) Run(ctx context.Context, p Project, args []string, w io.Writer) error {
	return DockerExecutor{}.Run(ctx, p, args, w)
}
func (x catalogIntegrationExecutor) Output(ctx context.Context, p Project, args []string, w io.Writer) error {
	var stderr bytes.Buffer
	err := dockerCommand(ctx, p, args, w, &stderr, "")
	if err != nil {
		x.t.Logf("Integration Docker %v diagnostic: %s", args[:2], stderr.String())
	}
	return err
}
