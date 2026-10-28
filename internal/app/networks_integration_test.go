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

// Explicit opt-in: mutate only unique fixture networks and containers created here.
func TestDockerNetworksIntegration(t *testing.T) {
	if os.Getenv("REEF_TEST_NETWORKS") != "1" {
		t.Skip("opt-in local Docker networks fixture")
	}
	name := "reef-network-test-" + fmt.Sprint(time.Now().UnixNano())
	other := name + "-base"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v: %v %s", args, err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	defer func() {
		_ = exec.Command("docker", "container", "rm", "--force", name).Run()
		_ = exec.Command("docker", "network", "rm", name).Run()
		_ = exec.Command("docker", "network", "rm", other).Run()
	}()
	s := &ImageService{Exec: catalogIntegrationExecutor{t}}
	results := s.runNetworks(ctx, "create", name, nil, "")
	if len(results) != 1 || !results[0].OK {
		t.Fatal(results)
	}
	docker("network", "create", other)
	id := docker("run", "-d", "--name", name, "--network", other, "--entrypoint", "sh", "docker:29.9.2-cli", "-c", "sleep 120")
	find := func() CatalogNetwork {
		t.Helper()
		rows, _, err := s.networks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Name == name {
				return row
			}
		}
		t.Fatal("fixture network missing")
		return CatalogNetwork{}
	}
	row := find()
	if !row.Removable || !row.Mutable {
		t.Fatal(row)
	}
	target := NetworkTarget{row.ID, row.Fingerprint}
	if err := s.mutateNetwork(ctx, "connect", target, id); err != nil {
		t.Fatal(err)
	}
	row = find()
	if row.Removable || len(row.Containers) != 1 {
		t.Fatal(row)
	}
	docker("stop", name)
	row = find()
	if row.Removable || len(row.Containers) != 1 {
		t.Fatal("stopped usage lost", row)
	}
	if err := s.mutateNetwork(ctx, "remove", target, ""); err == nil {
		t.Fatal("removed stopped container network")
	}
	docker("start", name)
	if err := s.mutateNetwork(ctx, "disconnect", target, id); err != nil {
		t.Fatal(err)
	}
	row = find()
	if !row.Removable {
		t.Fatal(row)
	}
	if err := s.mutateNetwork(ctx, "remove", target, ""); err != nil {
		t.Fatal(err)
	}
	docker("network", "create", name)
	if err := s.mutateNetwork(ctx, "remove", target, ""); err == nil {
		t.Fatal("stale target accepted")
	}
	t.Log("Verified network creation/addressing, live connect/disconnect, stopped references, immutable IDs and targeted removal")
}
