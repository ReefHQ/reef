package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Applied only by up, after verifying the local engine and installation.
func (l *Launcher) ensureRestartPolicy() error {
	path := filepath.Join(l.runtime(), "compose.json")
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() {
		return errors.New("Reef compose.json must be a regular file, not a symlink")
	}
	original, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	var compose map[string]any
	if e = json.Unmarshal(original, &compose); e != nil {
		return e
	}
	services, ok := compose["services"].(map[string]any)
	if !ok {
		return errors.New("Cannot find Reef services in compose.json")
	}
	service, ok := services["reef"].(map[string]any)
	if !ok {
		return errors.New("Cannot find the Reef service in compose.json")
	}
	if service["restart"] == "unless-stopped" {
		return nil
	}
	service["restart"] = "unless-stopped"
	if e = l.writeCompose(original, compose, info); e != nil {
		return e
	}
	fmt.Fprintln(l.out, "Reef restart policy: unless-stopped.")
	return nil
}
