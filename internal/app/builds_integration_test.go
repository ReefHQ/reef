package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in only: a unique scratch image/context and exact new cache record IDs.
// No RUN step, deployment, existing tag replacement, builder creation or broad prune.
func TestDockerBuildAndTargetedCacheIntegration(t *testing.T) {
	if os.Getenv("REEF_TEST_BUILDS") != "1" {
		t.Skip("opt-in development Docker build fixture")
	}
	m, p, _, _ := projectBuildFixture(t)
	name := "reef-build-test-" + fmt.Sprint(time.Now().UnixNano())
	tag := name + ":fixture"
	p.ProjectName = name
	m.Projects[p.ID].Project = p
	for file, content := range map[string]string{"compose.yaml": "services:\n  web:\n    image: " + tag + "\n    build: .\n", "Dockerfile": "FROM scratch\nLABEL io.reef.build-test=" + name + "\nCOPY marker /marker\n", "marker": name, ".dockerignore": "*\n!Dockerfile\n!marker\n"} {
		if err := os.WriteFile(filepath.Join(p.Directory, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _ = exec.Command("docker", "image", "rm", tag).Run() }()
	m.Exec = DockerExecutor{}
	m.Images = &ImageService{Exec: DockerExecutor{}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	baseline, err := m.Images.cacheRecords(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]bool{}
	for _, row := range baseline {
		before[row.ID] = true
	}
	rev, err := revision(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.beginOptions(p.ID, "build", rev, "", BuildOptions{NoCache: true}); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	op := m.Projects[p.ID].Op
	if op.Phase != "completed" || op.NeedsStart {
		t.Fatal(op.Result, op.Log)
	}
	if err = exec.Command("docker", "image", "inspect", tag).Run(); err != nil {
		t.Fatal("built image missing", err)
	}
	if err = exec.Command("docker", "image", "rm", tag).Run(); err != nil {
		t.Fatal(err)
	}
	records, err := m.Images.cacheRecords(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	target := BuildCacheRecord{}
	for _, row := range records {
		if !before[row.ID] && row.Reclaimable && !row.Mutable && row.Size > 0 && !row.Shared {
			target = row
			break
		}
	}
	if target.ID == "" {
		t.Fatal("fixture produced no new immutable/reclaimable cache record")
	}
	t.Logf("Fixture cache target: id=%s size=%d shared=%v mutable=%v", target.ID, target.Size, target.Shared, target.Mutable)
	if err = m.Images.removeCacheRecord(ctx, CacheTarget{target.ID, target.Fingerprint}); err != nil {
		t.Fatal(err)
	}
	remaining, err := m.Images.cacheRecords(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	after := map[string]bool{}
	for _, row := range remaining {
		after[row.ID] = true
	}
	if after[target.ID] {
		t.Fatal("selected fixture cache still present")
	}
	for id := range before {
		if !after[id] {
			t.Fatal("unselected preexisting cache removed", id)
		}
	}
	history, err := m.buildHistory(m.Projects[p.ID])
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	if strings.Contains(op.Log, "compose up") || strings.Contains(op.Log, "compose down") {
		t.Fatal("build deployed containers")
	}
	t.Log("Verified Compose build with default builder, persisted logs/history, exact fixture cache deletion and preservation of all preexisting cache records")
}
