package local

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T) *Launcher {
	t.Helper()
	l, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	l.getenv = func(string) string { return "" }
	l.out = io.Discard
	return l
}
func TestOverrideRefusedBeforeDocker(t *testing.T) {
	l := fixture(t)
	l.getenv = func(k string) string {
		if k == "DOCKER_HOST" {
			return "ssh://remote"
		}
		return ""
	}
	l.run = func(...string) ([]byte, error) { t.Fatal("Docker called despite override"); return nil, nil }
	if _, e := l.localEngine(); e == nil {
		t.Fatal("override accepted")
	}
}
func TestRemoteContextRefusedBeforeDaemon(t *testing.T) {
	l := fixture(t)
	var calls [][]string
	l.run = func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		if len(calls) == 1 {
			return []byte("remote\n"), nil
		}
		return []byte(`[{"Endpoints":{"docker":{"Host":"ssh://remote"}}}]`), nil
	}
	if _, e := l.localEngine(); e == nil {
		t.Fatal("remote accepted")
	}
	if !reflect.DeepEqual(calls, [][]string{{"context", "show"}, {"context", "inspect", "remote"}}) {
		t.Fatal(calls)
	}
}
func TestExistingRuntimeWithoutMetadataPreserved(t *testing.T) {
	l := fixture(t)
	os.MkdirAll(l.runtime(), 0700)
	p := filepath.Join(l.runtime(), "keep.txt")
	os.WriteFile(p, []byte("precious"), 0600)
	if _, e := l.initialize(engine{}); e == nil {
		t.Fatal("existing runtime accepted")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "precious" {
		t.Fatal("file changed")
	}
}
func TestEngineChangeRefused(t *testing.T) {
	m := metadata{Prefix: "reef-local-test", Image: "reef:reef-local-test", Port: 8099, Context: "desktop-linux", Endpoint: "unix:///var/run/docker.sock", EngineID: "original"}
	if e := validEngine(m, engine{"desktop-linux", "unix:///var/run/docker.sock", "changed"}); e == nil {
		t.Fatal("changed engine accepted")
	}
}
func TestFreshInstallationHasNoManagedProjectsAndNeverOverwritesPassword(t *testing.T) {
	l := fixture(t)
	os.MkdirAll(filepath.Join(l.runtime(), "tools"), 0700)
	_, e := l.initialize(engine{"desktop-linux", "unix:///var/run/docker.sock", "engine"})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(l.runtime(), "config.json"))
	var config struct{ Projects []any }
	if e = json.Unmarshal(b, &config); e != nil || config.Projects == nil || len(config.Projects) != 0 {
		t.Fatal(string(b), e)
	}
	var compose struct {
		Services map[string]struct {
			Restart     string
			Environment map[string]string
			Volumes     []struct {
				Type, Source, Target string
				ReadOnly             bool `json:"read_only"`
				Bind                 struct {
					CreateHostPath bool `json:"create_host_path"`
				}
			}
		}
	}
	install, _ := os.ReadFile(filepath.Join(l.runtime(), "compose.json"))
	if e = json.Unmarshal(install, &compose); e != nil {
		t.Fatal(e)
	}
	reef := compose.Services["reef"]
	if reef.Restart != "unless-stopped" {
		t.Fatal("fresh installation lacks unless-stopped restart policy")
	}
	if reef.Environment["REEF_INSTALLATION_DIR"] != l.root {
		t.Fatal("fresh installation path not transmitted")
	}
	found := false
	for _, mount := range reef.Volumes {
		if mount.Source == filepath.Dir(l.root) && mount.Target == filepath.Dir(l.root) && mount.Type == "bind" && !mount.ReadOnly && !mount.Bind.CreateHostPath {
			found = true
		}
	}
	if !found {
		t.Fatal("fresh installation parent not mounted")
	}
	pw, _ := os.ReadFile(filepath.Join(l.runtime(), "password.txt"))
	if len(strings.TrimSpace(string(pw))) < 16 {
		t.Fatal("weak password")
	}
	if _, e = l.initialize(engine{}); e == nil {
		t.Fatal("reinitialization accepted")
	}
	after, _ := os.ReadFile(filepath.Join(l.runtime(), "password.txt"))
	if string(pw) != string(after) {
		t.Fatal("password changed")
	}
}
func TestPasswordCanBeReadWithoutDocker(t *testing.T) {
	l := fixture(t)
	os.MkdirAll(l.runtime(), 0700)
	os.WriteFile(filepath.Join(l.runtime(), "password.txt"), []byte("private-password\n"), 0600)
	l.run = func(...string) ([]byte, error) { t.Fatal("password action contacted Docker"); return nil, nil }
	if e := l.Run("password"); e != nil {
		t.Fatal(e)
	}
}

func TestLocalLinuxEngineDoesNotRequireDockerDesktop(t *testing.T) {
	for _, tc := range []struct {
		name, platform, endpoint, osType, daemonName, daemonOS string
		accept                                                 bool
	}{
		{"Linux Engine", "linux", "unix:///var/run/docker.sock", "linux", "ubuntu-server", "Ubuntu 24.04", true},
		{"Linux rootless", "linux", "unix:///run/user/1000/docker.sock", "linux", "server", "Debian GNU/Linux", true},
		{"Linux custom socket", "linux", "unix:///srv/docker/engine.sock", "linux", "server", "Linux", true},
		{"macOS Desktop", "darwin", "unix:///Users/test/.docker/run/docker.sock", "linux", "docker-desktop", "Docker Desktop", true},
		{"macOS Colima", "darwin", "unix:///Users/test/.colima/default/docker.sock", "linux", "colima", "Ubuntu", true},
		{"macOS OrbStack", "darwin", "unix:///Users/test/.orbstack/run/docker.sock", "linux", "orbstack", "Linux", true},
		{"Windows containers", "linux", "unix:///var/run/docker.sock", "windows", "server", "Windows Server", false},
		{"TCP endpoint", "linux", "tcp://localhost:2375", "linux", "server", "Linux", false},
		{"SSH endpoint", "darwin", "ssh://server", "linux", "server", "Linux", false},
		{"URL authority", "linux", "unix://server/var/run/docker.sock", "linux", "server", "Linux", false},
		{"Noncanonical path", "linux", "unix:///run/../var/run/docker.sock", "linux", "server", "Linux", false},
		{"Query string", "linux", "unix:///var/run/docker.sock?host=remote", "linux", "server", "Linux", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := fixture(t)
			l.platform = tc.platform
			l.run = func(args ...string) ([]byte, error) {
				switch args[0] {
				case "context":
					if args[1] == "show" {
						return []byte("local\n"), nil
					}
					return json.Marshal([]any{map[string]any{"Endpoints": map[string]any{"docker": map[string]string{"Host": tc.endpoint}}}})
				default:
					return json.Marshal(map[string]string{"ID": "engine-id", "Name": tc.daemonName, "OperatingSystem": tc.daemonOS, "OSType": tc.osType})
				}
			}
			current, err := l.localEngine()
			if (err == nil) != tc.accept {
				t.Fatal(current, err)
			}
		})
	}
}
func TestSelectedSocketIsMountedForNativeAndVMEngines(t *testing.T) {
	for _, tc := range []struct {
		name, platform, endpoint, want string
		vm                             bool
	}{
		{"Linux rootless", "linux", "unix:///run/user/1000/docker.sock", "/run/user/1000/docker.sock", false},
		{"Linux custom", "linux", "unix:///srv/engine.sock", "/srv/engine.sock", false},
		{"macOS VM", "darwin", "unix:///Users/test/.colima/default/docker.sock", "/var/run/docker.sock", true},
		{"Linux Desktop VM", "linux", "unix:///home/test/.docker/desktop/docker.sock", "/var/run/docker.sock", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := fixture(t)
			l.platform = tc.platform
			l.daemonVM = tc.vm
			if _, err := l.initialize(engine{Context: "local", Endpoint: tc.endpoint, ID: "engine"}); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(l.runtime(), "compose.json"))
			if err != nil {
				t.Fatal(err)
			}
			var compose struct {
				Services map[string]struct {
					Volumes []struct{ Source, Target string }
				}
			}
			if json.Unmarshal(raw, &compose) != nil {
				t.Fatal("invalid Compose")
			}
			found := false
			for _, mount := range compose.Services["reef"].Volumes {
				if mount.Target == "/var/run/docker.sock" {
					found = mount.Source == tc.want
				}
			}
			if !found {
				t.Fatal("wrong engine socket mount", string(raw))
			}
		})
	}
}

func TestContextAndEndpointChangesRemainRejected(t *testing.T) {
	meta := metadata{Prefix: "reef-local-test", Image: "reef:reef-local-test", Port: 8099, Context: "desktop-linux", Endpoint: "unix:///var/run/docker.sock", EngineID: "engine"}
	for _, current := range []engine{{"colima", meta.Endpoint, meta.EngineID}, {meta.Context, "unix:///another.sock", meta.EngineID}} {
		if err := validEngine(meta, current); err == nil {
			t.Fatal("installation changed engine binding", current)
		}
	}
}
