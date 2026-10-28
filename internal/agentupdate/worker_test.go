package agentupdate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type workerFixture struct {
	w                                                               *Worker
	o                                                               Operation
	commands                                                        []string
	env                                                             []byte
	pullFail, wrongRevision, wrongVolume, changeConfig, noReconnect bool
	recreated                                                       bool
}

func newWorkerFixture(t *testing.T) *workerFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	installation := filepath.Join(root, "reef-agent")
	os.Mkdir(installation, 0700)
	env := []byte("COMPOSE_PROJECT_NAME=existing-agent\nREEF_AGENT_IMAGE=" + Repository + ":v0.10.0\nREEF_SERVER=https://private.example\nOTHER=private-setting\n")
	for name, content := range map[string][]byte{".env": env, "compose.yaml": []byte("fixture compose"), "compose.updates.yaml": []byte("fixture update overlay")} {
		if err = os.WriteFile(filepath.Join(installation, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(filepath.Join(root, "updates"))
	if err != nil {
		t.Fatal(err)
	}
	f := &workerFixture{env: env, o: Operation{ID: strings.Repeat("a", 48), FromVersion: "0.10.0", Version: "0.11.0", Revision: strings.Repeat("b", 40), Started: time.Now(), Phase: "queued"}}
	f.w = &Worker{Store: store, Installation: installation}
	f.w.Run = func(ctx context.Context, args []string) ([]byte, error) {
		command := strings.Join(args, " ")
		f.commands = append(f.commands, command)
		marshal := func(v any) ([]byte, error) { return json.Marshal(v) }
		switch {
		case args[0] == "compose" && strings.Contains(command, " config "):
			return marshal(map[string]any{"name": "existing-agent", "services": map[string]any{"reef-agent": map[string]any{"image": Repository + ":v0.10.0", "volumes": []map[string]string{{"type": "volume", "source": "agent-state", "target": "/var/lib/reef-agent"}}}}, "volumes": map[string]any{"agent-state": map[string]string{"name": "existing-agent_agent-state"}}})
		case args[0] == "compose" && strings.Contains(command, " ps "):
			return []byte(strings.Repeat("c", 64)), nil
		case args[0] == "container":
			volume := "existing-agent_agent-state"
			if f.wrongVolume {
				volume = "another-identity"
			}
			image := "sha256:" + strings.Repeat("d", 64)
			if f.recreated {
				image = "sha256:" + strings.Repeat("e", 64)
			}
			return marshal([]any{map[string]any{"Id": strings.Repeat("c", 64), "Image": image, "Config": map[string]any{"Labels": map[string]string{"com.docker.compose.project": "existing-agent", "com.docker.compose.service": "reef-agent", "org.opencontainers.image.version": "0.10.0"}}, "State": map[string]bool{"Running": true}, "Mounts": []any{map[string]string{"Type": "volume", "Name": volume, "Destination": "/var/lib/reef-agent"}}}})
		case args[0] == "pull":
			if f.pullFail {
				return nil, errors.New("private Docker error must not be returned")
			}
			if f.changeConfig {
				os.WriteFile(filepath.Join(installation, "compose.yaml"), []byte("external edit"), 0600)
			}
			return nil, nil
		case args[0] == "image":
			rev := f.o.Revision
			if f.wrongRevision {
				rev = strings.Repeat("f", 40)
			}
			return marshal([]any{map[string]any{"Id": "sha256:" + strings.Repeat("e", 64), "Config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.version": f.o.Version, "org.opencontainers.image.revision": rev}}}})
		case args[0] == "compose" && strings.Contains(command, " up "):
			f.recreated = true
			if !f.noReconnect {
				err := store.Write("connection.json", Connection{Version: f.o.Version, Revision: f.o.Revision, At: time.Now()})
				if err != nil {
					t.Fatal(err)
				}
			}
			return nil, nil
		default:
			t.Fatalf("Unexpected command: %v", args)
			return nil, nil
		}
	}
	return f
}
func TestUpdatePreservesIdentityAndSettingsAndWaitsForExactReconnect(t *testing.T) {
	f := newWorkerFixture(t)
	if err := f.w.execute(context.Background(), &f.o); err != nil {
		t.Fatal(err)
	}
	if f.o.Phase != "completed" {
		t.Fatal(f.o)
	}
	env, _ := os.ReadFile(filepath.Join(f.w.Installation, ".env"))
	expected := strings.Replace(string(f.env), ":v0.10.0", ":v0.11.0", 1)
	if string(env) != expected {
		t.Fatal("Private settings changed")
	}
	info, _ := os.Stat(filepath.Join(f.w.Installation, ".env"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("Private environment permissions changed")
	}
	for _, command := range f.commands {
		if strings.Contains(command, " up ") && !strings.HasSuffix(command, "up -d --no-deps --no-build --pull never reef-agent") {
			t.Fatal(command)
		}
		if strings.Contains(command, "down") || strings.Contains(command, "prune") || strings.Contains(command, "--renew-anon-volumes") {
			t.Fatal(command)
		}
	}
}
func TestPreflightFailuresNeverRecreateOrChangePrivateEnvironment(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*workerFixture)
	}{
		{"pull", func(f *workerFixture) { f.pullFail = true }},
		{"revision", func(f *workerFixture) { f.wrongRevision = true }},
		{"identity volume", func(f *workerFixture) { f.wrongVolume = true }},
		{"external edit", func(f *workerFixture) { f.changeConfig = true }},
		{"downgrade", func(f *workerFixture) { f.o.Version = "0.9.0" }},
		{"expired", func(f *workerFixture) { f.o.Started = time.Now().Add(-2 * time.Minute) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newWorkerFixture(t)
			test.setup(f)
			if err := f.w.execute(context.Background(), &f.o); err == nil {
				t.Fatal("Unsafe update accepted")
			}
			if f.recreated {
				t.Fatal("Existing agent recreated")
			}
			env, _ := os.ReadFile(filepath.Join(f.w.Installation, ".env"))
			if string(env) != string(f.env) {
				t.Fatal("Private configuration modified")
			}
		})
	}
}
func TestMissingOrWrongReconnectNeverReportsSuccess(t *testing.T) {
	f := newWorkerFixture(t)
	f.noReconnect = true
	f.w.Store.Write("connection.json", Connection{Version: f.o.Version, Revision: strings.Repeat("f", 40), At: time.Now()})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := f.w.execute(ctx, &f.o); err == nil || f.o.Phase == "completed" {
		t.Fatal("Mismatched reconnection accepted")
	}
}
func TestWorkerRestartDoesNotReplayAcceptedRequests(t *testing.T) {
	f := newWorkerFixture(t)
	if err := f.w.Store.Write("operation.json", f.o); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := f.w.Serve(ctx); err != nil {
		t.Fatal(err)
	}
	o, err := f.w.Store.Operation()
	if err != nil || o.Phase != "unknown" || len(f.commands) != 0 {
		t.Fatal(o, err, f.commands)
	}
}
func TestStateRejectsSymlinksAndValidatesVersions(t *testing.T) {
	f := newWorkerFixture(t)
	os.Symlink(filepath.Join(f.w.Installation, ".env"), filepath.Join(f.w.Store.Dir, "operation.json"))
	if _, err := f.w.Store.Operation(); err == nil {
		t.Fatal("Symlinked state accepted")
	}
	if err := f.w.Store.Write("operation.json", f.o); err == nil {
		t.Fatal("Symlinked state replaced")
	}
	for _, version := range []string{"dev", "latest", "01.2.3", "1.2.3;id", "1.2"} {
		if ValidTarget(version, strings.Repeat("a", 40)) {
			t.Fatal(version)
		}
	}
	if !Newer("0.10.0", "0.9.9") || Newer("0.10.0", "0.10.0") || Newer("0.9.9", "0.10.0") {
		t.Fatal("Incorrect version ordering")
	}
}
