package local

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNetworkModeChangesOnlyReefBindingAndPreservesPrivateFiles(t *testing.T) {
	l := legacyInstallation(t)
	private := map[string]string{"password.txt": "private fixture password", "state/keep": "private history", "config.json": "{\"projects\":[],\"allowed_project_roots\":[]}"}
	for name, content := range private {
		path := filepath.Join(l.runtime(), name)
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte(content), 0600)
	}
	path := filepath.Join(l.runtime(), "compose.json")
	original, _ := os.ReadFile(path)
	if err := l.ensureNetworkMode("lan"); err != nil {
		t.Fatal(err)
	}
	var expected, got map[string]any
	json.Unmarshal(original, &expected)
	expected["services"].(map[string]any)["reef"].(map[string]any)["ports"] = []any{"0.0.0.0:54321:8099"}
	after, _ := os.ReadFile(path)
	json.Unmarshal(after, &got)
	if !reflect.DeepEqual(expected, got) {
		t.Fatal("Network mode changed unrelated Compose settings")
	}
	if err := l.ensureNetworkMode("lan"); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(after) {
		t.Fatal("LAN mode is not idempotent")
	}
	if err := l.ensureNetworkMode(""); err != nil {
		t.Fatal(err)
	}
	again, _ = os.ReadFile(path)
	if string(again) != string(after) {
		t.Fatal("Default launch changed the configured network mode")
	}
	if err := l.ensureNetworkMode("local"); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(path)
	json.Unmarshal(restored, &got)
	json.Unmarshal(original, &expected)
	if !reflect.DeepEqual(expected, got) {
		t.Fatal("Local mode did not restore loopback binding")
	}
	for name, content := range private {
		b, _ := os.ReadFile(filepath.Join(l.runtime(), name))
		if string(b) != content {
			t.Fatal("Private runtime changed", name)
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("Compose permissions changed")
	}
}
func TestNetworkModeSupportsExplicitLongAndIPv6Mappings(t *testing.T) {
	for _, ports := range [][]any{
		{"54321:8099/tcp", "127.0.0.1:5555:9090"},
		{"[::1]:54321:8099", "5555:9090/udp"},
		{map[string]any{"target": 8099, "published": "54321", "host_ip": "127.0.0.1", "protocol": "tcp", "mode": "host"}},
	} {
		l := legacyInstallation(t)
		path := filepath.Join(l.runtime(), "compose.json")
		b, _ := os.ReadFile(path)
		var c map[string]any
		json.Unmarshal(b, &c)
		service := c["services"].(map[string]any)["reef"].(map[string]any)
		service["ports"] = ports
		b, _ = json.Marshal(c)
		os.WriteFile(path, b, 0600)
		if err := l.ensureNetworkMode("lan"); err != nil {
			t.Fatal(err)
		}
		b, _ = os.ReadFile(path)
		json.Unmarshal(b, &c)
		got := c["services"].(map[string]any)["reef"].(map[string]any)["ports"].([]any)
		if len(got) != len(ports) {
			t.Fatal(got)
		}
		if first, ok := got[0].(string); ok {
			if first != "0.0.0.0:54321:8099" && first != "0.0.0.0:54321:8099/tcp" {
				t.Fatal(first)
			}
			if len(got) > 1 && got[1] != ports[1] {
				t.Fatal("Other port changed")
			}
		} else {
			first := got[0].(map[string]any)
			if first["host_ip"] != "0.0.0.0" || first["published"] != "54321" || first["mode"] != "host" {
				t.Fatal(first)
			}
		}
	}
}
func TestNetworkModeRejectsAmbiguousDynamicAndSymlinkedConfigurations(t *testing.T) {
	for _, ports := range [][]any{{"8099"}, {"0:8099"}, {"127.0.0.1:8099:8099", "[::1]:8099:8099"}, {"8099:8099/udp"}} {
		l := legacyInstallation(t)
		path := filepath.Join(l.runtime(), "compose.json")
		b, _ := os.ReadFile(path)
		var c map[string]any
		json.Unmarshal(b, &c)
		c["services"].(map[string]any)["reef"].(map[string]any)["ports"] = ports
		b, _ = json.Marshal(c)
		os.WriteFile(path, b, 0600)
		if err := l.ensureNetworkMode("lan"); err == nil {
			t.Fatal("Unsupported mapping accepted", ports)
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(b) {
			t.Fatal("Rejected mapping changed Compose")
		}
	}
	l := legacyInstallation(t)
	path := filepath.Join(l.runtime(), "compose.json")
	os.Rename(path, path+".original")
	os.Symlink(path+".original", path)
	if err := l.ensureNetworkMode("lan"); err == nil {
		t.Fatal("Symlinked Compose accepted")
	}
}
func TestNetworkFlagsRequireUpAndRejectConflictsBeforeDocker(t *testing.T) {
	for _, tc := range []struct {
		action  string
		options []string
	}{{"status", []string{"--lan"}}, {"stop", []string{"--local"}}, {"password", []string{"--lan"}}, {"up", []string{"--lan", "--local"}}, {"up", []string{"--unknown"}}} {
		l := fixture(t)
		l.run = func(...string) ([]byte, error) { t.Fatal("Docker called for invalid options"); return nil, nil }
		if err := l.Run(tc.action, tc.options...); err == nil {
			t.Fatal(tc)
		}
	}
}
func TestFreshLANInstallationUsesWildcardAndExistingRuntimeIsPreserved(t *testing.T) {
	l := fixture(t)
	if _, err := l.initialize(engine{Context: "fixture", Endpoint: "unix:///var/run/docker.sock", ID: "fixture"}, "lan"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(l.runtime(), "compose.json"))
	var c map[string]any
	json.Unmarshal(b, &c)
	ports := c["services"].(map[string]any)["reef"].(map[string]any)["ports"].([]any)
	if len(ports) != 1 || len(ports[0].(string)) < 8 || ports[0].(string)[:8] != "0.0.0.0:" {
		t.Fatal(ports)
	}
}
