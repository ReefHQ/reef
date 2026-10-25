package local

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRestartPolicyMigrationOnlyChangesReefAndIsIdempotent(t *testing.T) {
	l := legacyInstallation(t)
	configPath := filepath.Join(l.runtime(), "config.json")
	config := []byte(`{"projects":[],"allowed_project_roots":[]}`)
	os.WriteFile(configPath, config, 0600)
	path := filepath.Join(l.runtime(), "compose.json")
	before, _ := os.ReadFile(path)
	var original map[string]any
	json.Unmarshal(before, &original)
	original["services"].(map[string]any)["untouched"].(map[string]any)["restart"] = "always"
	before, _ = json.Marshal(original)
	os.WriteFile(path, before, 0600)
	if e := l.ensureRestartPolicy(); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	var migrated map[string]any
	json.Unmarshal(after, &migrated)
	service := migrated["services"].(map[string]any)["reef"].(map[string]any)
	if service["restart"] != "unless-stopped" {
		t.Fatal(service["restart"])
	}
	delete(service, "restart")
	if !reflect.DeepEqual(original, migrated) {
		t.Fatal("Restart migration changed other service/configuration fields")
	}
	gotConfig, _ := os.ReadFile(configPath)
	if string(gotConfig) != string(config) {
		t.Fatal("Base configuration changed")
	}
	if e := l.ensureRestartPolicy(); e != nil {
		t.Fatal(e)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(after) {
		t.Fatal("Restart migration is not idempotent")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}

func TestRestartPolicyMigrationRejectsSymlink(t *testing.T) {
	l := legacyInstallation(t)
	path := filepath.Join(l.runtime(), "compose.json")
	if e := os.Rename(path, path+".original"); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(path+".original", path); e != nil {
		t.Fatal(e)
	}
	if e := l.ensureRestartPolicy(); e == nil {
		t.Fatal("Symlinked Compose file accepted")
	}
}
