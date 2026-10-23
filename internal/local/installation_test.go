package local

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyInstallation(t *testing.T) *Launcher {
	t.Helper()
	l := fixture(t)
	os.MkdirAll(l.runtime(), 0700)
	os.WriteFile(filepath.Join(l.runtime(), "config.json"), []byte("{\"projects\": [], \"custom-option\": \"keep\"}\n"), 0600)
	compose := map[string]any{"name": "keep-name", "services": map[string]any{"reef": map[string]any{"image": "keep-image", "environment": map[string]any{"QCU_PASSWORD_FILE": "keep-password-path"}, "volumes": []any{map[string]any{"type": "bind", "source": filepath.Join(l.runtime(), "project"), "target": filepath.Join(l.runtime(), "project"), "bind": map[string]any{"create_host_path": false}}}, "ports": []string{"127.0.0.1:54321:8099"}}, "untouched": map[string]any{"image": "never-invoked"}}, "x-preserved": map[string]any{"extra": "yes"}}
	b, _ := json.MarshalIndent(compose, "", "  ")
	os.WriteFile(filepath.Join(l.runtime(), "compose.json"), b, 0600)
	return l
}
func TestInstallationParentMigrationPreservesPrivateFilesAndIsIdempotent(t *testing.T) {
	l := legacyInstallation(t)
	paths := []string{"config.json", "password.txt", "state/registered-projects.json", "state/history.json"}
	before := map[string]string{}
	for _, path := range paths {
		full := filepath.Join(l.runtime(), path)
		if path != "config.json" {
			os.MkdirAll(filepath.Dir(full), 0700)
			os.WriteFile(full, []byte("precious-private-data"), 0600)
		}
		b, _ := os.ReadFile(full)
		before[path] = string(b)
	}
	if e := l.ensureInstallationRoot(); e != nil {
		t.Fatal(e)
	}
	var compose map[string]any
	b, _ := os.ReadFile(filepath.Join(l.runtime(), "compose.json"))
	json.Unmarshal(b, &compose)
	services := compose["services"].(map[string]any)
	reef := services["reef"].(map[string]any)
	env := reef["environment"].(map[string]any)
	if env["REEF_INSTALLATION_DIR"] != l.root || env["QCU_PASSWORD_FILE"] != "keep-password-path" || services["untouched"].(map[string]any)["image"] != "never-invoked" || compose["x-preserved"].(map[string]any)["extra"] != "yes" {
		t.Fatal("migration overwrote unrelated settings")
	}
	volumes := reef["volumes"].([]any)
	if len(volumes) != 2 {
		t.Fatal(volumes)
	}
	mount := volumes[1].(map[string]any)
	if mount["source"] != filepath.Dir(l.root) || mount["target"] != filepath.Dir(l.root) || mount["read_only"] != false || mount["bind"].(map[string]any)["create_host_path"] != false {
		t.Fatal(mount)
	}
	for _, path := range paths {
		after, _ := os.ReadFile(filepath.Join(l.runtime(), path))
		if string(after) != before[path] {
			t.Fatal("private file changed:", path)
		}
	}
	first := string(b)
	if e := l.ensureInstallationRoot(); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(filepath.Join(l.runtime(), "compose.json"))
	if string(b) != first {
		t.Fatal("migration not idempotent")
	}
	info, _ := os.Stat(filepath.Join(l.runtime(), "compose.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("mode changed")
	}
}
func TestExplicitRootsOverrideMigrationIncludingDisabledList(t *testing.T) {
	for _, roots := range []string{`[]`, `["/custom/chosen/root"]`} {
		t.Run(roots, func(t *testing.T) {
			l := legacyInstallation(t)
			path := filepath.Join(l.runtime(), "config.json")
			os.WriteFile(path, []byte(`{"allowed_project_roots":`+roots+`}`), 0600)
			before, _ := os.ReadFile(filepath.Join(l.runtime(), "compose.json"))
			if e := l.ensureInstallationRoot(); e != nil {
				t.Fatal(e)
			}
			after, _ := os.ReadFile(filepath.Join(l.runtime(), "compose.json"))
			if string(after) != string(before) {
				t.Fatal("explicit roots or mounts modified")
			}
		})
	}
}
func TestParentMigrationRefusesCustomMountOrInstallationMismatch(t *testing.T) {
	for _, tc := range []string{"read-only", "different source", "short mount", "environment mismatch", "config mismatch"} {
		t.Run(tc, func(t *testing.T) {
			l := legacyInstallation(t)
			path := filepath.Join(l.runtime(), "compose.json")
			b, _ := os.ReadFile(path)
			var compose map[string]any
			json.Unmarshal(b, &compose)
			service := compose["services"].(map[string]any)["reef"].(map[string]any)
			mount := map[string]any{"type": "bind", "source": filepath.Dir(l.root), "target": filepath.Dir(l.root), "read_only": false, "bind": map[string]any{"create_host_path": false}}
			switch tc {
			case "read-only":
				mount["read_only"] = true
				service["volumes"] = []any{mount}
			case "different source":
				mount["source"] = "/different"
				service["volumes"] = []any{mount}
			case "short mount":
				service["volumes"] = []any{filepath.Dir(l.root) + ":" + filepath.Dir(l.root)}
			case "environment mismatch":
				service["environment"].(map[string]any)["REEF_INSTALLATION_DIR"] = "/different/reef"
			case "config mismatch":
				os.WriteFile(filepath.Join(l.runtime(), "config.json"), []byte(`{"installation_directory":"/different/reef"}`), 0600)
			}
			before, _ := json.Marshal(compose)
			os.WriteFile(path, before, 0600)
			if e := l.ensureInstallationRoot(); e == nil {
				t.Fatal("custom settings overwritten")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) {
				t.Fatal("partial migration committed")
			}
		})
	}
}
func TestMigrationAcceptsListEnvironmentAndNullRoots(t *testing.T) {
	l := legacyInstallation(t)
	os.WriteFile(filepath.Join(l.runtime(), "config.json"), []byte(`{"allowed_project_roots":null}`), 0600)
	path := filepath.Join(l.runtime(), "compose.json")
	b, _ := os.ReadFile(path)
	var compose map[string]any
	json.Unmarshal(b, &compose)
	service := compose["services"].(map[string]any)["reef"].(map[string]any)
	service["environment"] = []string{"KEEP=unchanged"}
	b, _ = json.Marshal(compose)
	os.WriteFile(path, b, 0600)
	if e := l.ensureInstallationRoot(); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "REEF_INSTALLATION_DIR="+l.root) || !strings.Contains(string(after), "KEEP=unchanged") {
		t.Fatal("environment list not preserved")
	}
}
