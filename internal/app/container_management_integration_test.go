package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type standaloneIntegrationExecutor struct{ source string }

func (x standaloneIntegrationExecutor) Run(ctx context.Context, p Project, args []string, w io.Writer) error {
	// Simulate registry delivery with two uniquely tagged real local images. The
	// workflow still resolves and pins Docker's real immutable image identity.
	if len(args) == 3 && args[0] == "image" && args[1] == "pull" {
		return (DockerExecutor{}).Run(ctx, p, []string{"image", "tag", x.source, args[2]}, w)
	}
	return (DockerExecutor{}).Run(ctx, p, args, w)
}
func TestDockerStandaloneContainerIntegration(t *testing.T) {
	if os.Getenv("REEF_TEST_STANDALONE") != "1" {
		t.Skip("opt-in disposable Docker standalone fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	name := "reef-standalone-test-" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	docker := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stdin = strings.NewReader(input)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("Fixture Docker command failed: %v %s", e, out)
		}
		return strings.TrimSpace(string(out))
	}
	oldTag, newTag, badTag := name+":old", name+":new", name+":bad"
	names := []string{name, name + "-failed"}
	volumes := []string{name + "-data"}
	images := []string{oldTag, newTag, badTag}
	defer func() {
		for _, n := range names {
			exec.Command("docker", "rm", "-f", n).Run()
		}
		for _, n := range volumes {
			exec.Command("docker", "volume", "rm", n).Run()
		}
		exec.Command("docker", "network", "rm", name).Run()
		for _, n := range images {
			exec.Command("docker", "image", "rm", n).Run()
		}
	}()
	for _, spec := range []struct{ tag, extra string }{{oldTag, "LABEL io.reef.fixture=old\n"}, {newTag, "LABEL io.reef.fixture=new\n"}, {badTag, "HEALTHCHECK --interval=1s --timeout=1s --retries=1 CMD exit 1\n"}} {
		docker("FROM docker:29.9.2-cli\nLABEL io.reefhq.standalone-test="+name+"\n"+spec.extra, "build", "--pull=false", "-q", "-t", spec.tag, "-")
		images = append(images, docker("", "image", "inspect", "--format", "{{.Id}}", spec.tag))
	}
	docker("", "network", "create", name)
	docker("", "volume", "create", volumes[0])
	for i, n := range names {
		id := docker("", "run", "-d", "--name", n, "--restart", "unless-stopped", "--network", name, "--network-alias", n+"-alias", "-p", "127.0.0.1::8080", "-e", "FIXTURE_SECRET=private-value", "-v", volumes[0]+":/data", "-v", "/anonymous", "--entrypoint", "sh", oldTag, "-c", "trap 'exit 0' TERM; while :; do sleep 1; done")
		ip := docker("", "inspect", "--format", `{{(index .NetworkSettings.Networks "`+name+`").IPAddress}}`, id)
		docker("", "network", "disconnect", name, id)
		docker("", "network", "connect", "--ip", ip, "--alias", n+"-alias", name, id)
		anonymous := docker("", "inspect", "--format", `{{range .Mounts}}{{if eq .Destination "/anonymous"}}{{.Name}}{{end}}{{end}}`, id)
		volumes = append(volumes, anonymous)
		docker("", "exec", id, "sh", "-c", "printf preserved > /data/marker; printf anonymous > /anonymous/marker")
		m, _, _ := fixture(t, &fakeExecutor{})
		source := newTag
		if i == 1 {
			source = badTag
		}
		m.Exec = standaloneIntegrationExecutor{source}
		s := newServer(m, Config{StateDir: m.StateDir}, "fixture-only-password-123")
		before, old, e := s.m.containers.review(ctx, id)
		if e != nil || !before.Allowed || !before.UpdateAllowed {
			t.Fatalf("Fixture not eligible: %+v %v", before, e)
		}
		started, e := m.beginContainer("update", id, before.Fingerprint)
		if e != nil {
			t.Fatal(e)
		}
		m.wg.Wait()
		op, e := m.containers.snapshot()
		if e != nil {
			t.Fatal(e)
		}
		if op.NewID != "" {
			names = append(names, op.TemporaryName, op.PreviousName)
		}
		if i == 0 {
			if !op.OK || op.Unknown {
				t.Fatalf("Update failed: %+v", op)
			}
			fresh, e := m.containers.inspect(ctx, op.NewID)
			if e != nil {
				t.Fatal(e)
			}
			if fresh.Name != old.Name || !fresh.State.Running || fresh.Image == old.Image || value[struct{ Name string }](fresh.HostConfig, "RestartPolicy").Name != "unless-stopped" {
				t.Fatal("Name/state/image/restart policy changed incorrectly")
			}
			if docker("", "exec", op.NewID, "cat", "/data/marker") != "preserved" || docker("", "exec", op.NewID, "cat", "/anonymous/marker") != "anonymous" {
				t.Fatal("Mount contents lost")
			}
			if value[[]string](fresh.Config, "Env")[0] == "" {
				t.Fatal("Environment missing")
			}
			expectedEnv := value[[]string](old.Config, "Env")
			gotEnv := value[[]string](fresh.Config, "Env")
			a, _ := json.Marshal(expectedEnv)
			b, _ := json.Marshal(gotEnv)
			if string(a) != string(b) {
				t.Fatal("Environment changed")
			}
			found := false
			for _, mount := range fresh.Mounts {
				if mount.Destination == "/anonymous" {
					found = mount.Name == anonymous
				}
			}
			if !found {
				t.Fatal("Anonymous volume identity changed")
			}
			if len(fresh.NetworkSettings.Networks) != len(old.NetworkSettings.Networks) {
				t.Fatal("Network attachments changed")
			}
			a, _ = json.Marshal(old.NetworkSettings.Ports)
			b, _ = json.Marshal(fresh.NetworkSettings.Ports)
			if string(a) != string(b) {
				t.Fatal("Published ports changed")
			}
			aliases := value[[]string](fresh.NetworkSettings.Networks[name], "Aliases")
			hasAlias := false
			for _, alias := range aliases {
				if alias == n+"-alias" {
					hasAlias = true
				}
			}
			if !hasAlias {
				t.Fatal("User network alias lost")
			}
			if value[string](fresh.NetworkSettings.Networks[name], "IPAddress") != ip {
				t.Fatal("Explicit IP address changed")
			}
			if _, e = m.containers.inspect(ctx, id); e == nil {
				t.Fatal("Previous container remains after success")
			}
			review, _, e := m.containers.review(ctx, op.NewID)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = m.beginContainer("remove", op.NewID, review.Fingerprint); e != nil {
				t.Fatal(e)
			}
			m.wg.Wait()
			deleted, e := m.containers.snapshot()
			if e != nil || !deleted.OK {
				t.Fatal("Deletion failed", deleted, e)
			}
			docker("", "volume", "inspect", volumes[0])
			docker("", "volume", "inspect", anonymous)
			docker("", "image", "inspect", newTag)
		} else {
			if op.OK || op.Unknown || op.Phase != "rolled_back" {
				t.Fatalf("Recovery failed: %+v", op)
			}
			restored, e := m.containers.inspect(ctx, id)
			if e != nil || restored.Name != old.Name || !restored.State.Running || value[struct{ Name string }](restored.HostConfig, "RestartPolicy").Name != "unless-stopped" || len(restored.NetworkSettings.Networks) != len(old.NetworkSettings.Networks) {
				t.Fatal("Original container not restored", e)
			}
		}
		_ = started
	}
	t.Log("Verified real recreation, name/restart policy, environment, named+anonymous volume identity/data, network aliases and unhealthy-start rollback. Registry pull simulated by local image retag only.")
}
