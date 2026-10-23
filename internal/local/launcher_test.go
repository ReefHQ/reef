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
	l.home = "/Users/test"
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
