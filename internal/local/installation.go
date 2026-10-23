package local

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ensureInstallationRoot migrates the previous implicit local default on `up`.
// It only reads Reef's two installation files. In particular it never enumerates
// the installation parent or candidate project directories. Explicit roots win.
func (l *Launcher) ensureInstallationRoot() error {
	configPath := filepath.Join(l.runtime(), "config.json")
	b, e := os.ReadFile(configPath)
	if e != nil {
		return e
	}
	var cfg map[string]json.RawMessage
	if e = json.Unmarshal(b, &cfg); e != nil {
		return e
	}
	if raw, ok := cfg["allowed_project_roots"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var roots []string
		if e = json.Unmarshal(raw, &roots); e != nil {
			return errors.New("Invalid explicit allowed_project_roots")
		}
		// Preserve custom roots, including an explicit [] that disables registration.
		// Do not add a broader parent mount when the default is not in use.
		return nil
	}
	if raw, ok := cfg["installation_directory"]; ok {
		var configured string
		if e = json.Unmarshal(raw, &configured); e != nil {
			return e
		}
		if configured != "" && configured != l.root {
			return errors.New("installation_directory differs from this local Reef folder; reconcile the explicit installation path before launching")
		}
	}
	parent := filepath.Dir(l.root)
	if parent == string(filepath.Separator) {
		return errors.New("Reef's installation parent cannot be the filesystem root")
	}
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
	changed := false
	switch env := service["environment"].(type) {
	case nil:
		service["environment"] = map[string]any{"REEF_INSTALLATION_DIR": l.root}
		changed = true
	case map[string]any:
		v, exists := env["REEF_INSTALLATION_DIR"]
		if exists && v != l.root {
			return errors.New("Explicit REEF_INSTALLATION_DIR differs from this local Reef folder; reconcile it before launching")
		}
		if !exists {
			env["REEF_INSTALLATION_DIR"] = l.root
			changed = true
		}
	case []any:
		exists := false
		for _, raw := range env {
			value, ok := raw.(string)
			if !ok {
				return errors.New("Invalid Reef environment entry")
			}
			if value == "REEF_INSTALLATION_DIR" || strings.HasPrefix(value, "REEF_INSTALLATION_DIR=") {
				if value != "REEF_INSTALLATION_DIR="+l.root {
					return errors.New("Explicit REEF_INSTALLATION_DIR differs from this local Reef folder; reconcile it before launching")
				}
				exists = true
			}
		}
		if !exists {
			service["environment"] = append(env, "REEF_INSTALLATION_DIR="+l.root)
			changed = true
		}
	default:
		return errors.New("Unsupported Reef environment format; preserve and check compose.json")
	}
	volumes, ok := service["volumes"].([]any)
	if service["volumes"] != nil && !ok {
		return errors.New("Unsupported Reef volumes format")
	}
	mounted := false
	for _, raw := range volumes {
		switch mount := raw.(type) {
		case map[string]any:
			if mount["target"] == parent {
				if mount["type"] != "bind" || mount["source"] != parent || mount["read_only"] == true {
					return errors.New("Existing mount at Reef's installation parent is not an identical-path writable bind; refusing to overwrite it")
				}
				bind, _ := mount["bind"].(map[string]any)
				if bind == nil {
					bind = map[string]any{}
					mount["bind"] = bind
				}
				if bind["create_host_path"] != false {
					bind["create_host_path"] = false
					changed = true
				}
				mounted = true
			}
		case string:
			parts := strings.Split(mount, ":")
			if len(parts) >= 2 && parts[1] == parent {
				return errors.New("Use an explicit long bind with identical absolute paths and create_host_path: false for Reef's installation parent; the launcher will not replace a custom short mount")
			}
		default:
			return errors.New("Unsupported Reef mount entry")
		}
	}
	if !mounted {
		service["volumes"] = append(volumes, map[string]any{"type": "bind", "source": parent, "target": parent, "read_only": false, "bind": map[string]any{"create_host_path": false}})
		changed = true
	}
	if !changed {
		return nil
	}
	next, e := json.MarshalIndent(compose, "", "  ")
	if e != nil {
		return e
	}
	next = append(next, '\n')
	f, e := os.CreateTemp(l.runtime(), ".reef-installation-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if e = f.Chown(int(stat.Uid), int(stat.Gid)); e != nil {
			f.Close()
			return e
		}
	}
	if e = f.Chmod(info.Mode().Perm()); e == nil {
		_, e = f.Write(next)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	current, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	if !bytes.Equal(current, original) {
		return errors.New("compose.json changed during installation migration; retry after reviewing it")
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	directory, e := os.Open(l.runtime())
	if e != nil {
		return e
	}
	defer directory.Close()
	if e = directory.Sync(); e != nil {
		return e
	}
	fmt.Fprintln(l.out, "Default project root: "+parent+" (parent of Reef installation "+l.root+"). Only Reef's service definition was updated; configured projects and private state were preserved.")
	return nil
}
