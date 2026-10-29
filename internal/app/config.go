package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	VolumeBackupDirectory   string    `json:"volume_backup_directory,omitempty"`
	VolumeBackupRetention   int       `json:"volume_backup_retention,omitempty"`
	AgentsEnabled           bool      `json:"agents_enabled,omitempty"`
	Listen                  string    `json:"listen"`
	StateDir                string    `json:"state_dir"`
	SecureCookie            bool      `json:"secure_cookie"`
	Projects                []Project `json:"projects"`
	AllowedProjectRoots     []string  `json:"allowed_project_roots"`
	InstallationDirectory   string    `json:"installation_directory,omitempty"`
	ConsoleSessionsPerLogin int       `json:"console_sessions_per_login,omitempty"`
	ConsoleSessionsTotal    int       `json:"console_sessions_total,omitempty"`
}
type Project struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Directory    string   `json:"directory"`
	ComposeFiles []string `json:"compose_files"`
	ProjectName  string   `json:"project_name"`
	EnvFiles     []string `json:"env_files,omitempty"`
}

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func loadConfig(path string) (Config, error) { return loadConfigState(path, "") }
func loadConfigState(path, stateOverride string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, e
	}
	if stateOverride != "" {
		c.StateDir = stateOverride
	}
	return finishConfig(c)
}
func finishConfig(c Config) (Config, error) {
	if c.VolumeBackupRetention < 0 || c.VolumeBackupRetention > 100 {
		return c, errors.New("volume_backup_retention must be 1–100, or 0 for the default of 7")
	}
	if p := c.VolumeBackupDirectory; p != "" && (!filepath.IsAbs(p) || filepath.Clean(p) != p || p == string(filepath.Separator)) {
		return c, errors.New("volume_backup_directory must be a canonical absolute directory")
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8099"
	}
	if e := validateConsoleLimits(c); e != nil {
		return c, e
	}
	if !filepath.IsAbs(c.StateDir) {
		return c, errors.New("state_dir must be absolute")
	}
	if c.InstallationDirectory == "" {
		c.InstallationDirectory = os.Getenv("REEF_INSTALLATION_DIR")
	}
	if c.AllowedProjectRoots == nil && c.InstallationDirectory != "" {
		dir := c.InstallationDirectory
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || filepath.Dir(dir) == string(filepath.Separator) {
			return c, errors.New("installation_directory must be canonical and absolute; its parent cannot be the filesystem root")
		}
		if e := rejectSymlinks(dir); e != nil {
			return c, errors.New("Reef installation directory is unavailable or contains symlinks; mount its parent at the identical absolute host path")
		}
		info, e := os.Stat(dir)
		if e != nil || !info.IsDir() {
			return c, errors.New("Reef installation_directory must be an existing directory")
		}
	}
	if e := validateRoots(c.AllowedProjectRoots); e != nil {
		return c, e
	}
	if e := validateProjects(c.Projects); e != nil {
		return c, e
	}
	return c, nil
}
func (c Config) consoleLimits() (int, int) {
	per, total := c.ConsoleSessionsPerLogin, c.ConsoleSessionsTotal
	if per == 0 {
		per = 8
	}
	if total == 0 {
		total = 16
	}
	return per, total
}
func validateConsoleLimits(c Config) error {
	per, total := c.consoleLimits()
	if per < 1 || per > 64 || total < 1 || total > 128 || per > total {
		return errors.New("console session limits must be 1–64 per login and 1–128 total, with per-login no greater than total")
	}
	return nil
}
func validateProjects(projects []Project) error {
	ids, identities, files := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, p := range projects {
		if !identifier.MatchString(p.ID) || !identifier.MatchString(p.ProjectName) || ids[p.ID] || identities[p.ProjectName] {
			return errors.New("Invalid or duplicate ID or Compose project name")
		}
		ids[p.ID], identities[p.ProjectName] = true, true
		if e := validateProjectFiles(p); e != nil {
			return e
		}
		for _, f := range p.ComposeFiles {
			full := filepath.Join(p.Directory, f)
			if files[full] {
				return errors.New("Compose files cannot be shared between registered projects")
			}
			files[full] = true
		}
	}
	return nil
}
func validateProjectFiles(p Project) error {
	if !filepath.IsAbs(p.Directory) || filepath.Clean(p.Directory) != p.Directory || len(p.ComposeFiles) == 0 {
		if len(p.ComposeFiles) == 0 {
			return &projectFieldError{"compose_files", errors.New("At least one Compose file is required")}
		}
		return &projectFieldError{"directory", errors.New("A canonical absolute project directory is required")}
	}
	e := rejectSymlinks(p.Directory)
	if e != nil {
		return &projectFieldError{"directory", errors.New("Project folder is unavailable. Mount it in Reef at the identical absolute host path; no folder was created")}
	}
	info, e := os.Stat(p.Directory)
	if e != nil || !info.IsDir() {
		return &projectFieldError{"directory", errors.New("Project folder is not a directory")}
	}
	fs := append(append([]string{}, p.ComposeFiles...), p.EnvFiles...)
	fs = append(fs, ".env")
	seen := map[string]bool{}
	for i, f := range fs {
		field := "compose_files"
		if i >= len(p.ComposeFiles) {
			field = "env_files"
		}
		if i < len(p.ComposeFiles) {
			if seen[f] {
				return &projectFieldError{field, errors.New("Duplicate Compose file")}
			}
			seen[f] = true
		}
		if f == "." || f == ".." || filepath.IsAbs(f) || filepath.Clean(f) != f || !within(p.Directory, filepath.Join(p.Directory, f)) {
			return &projectFieldError{field, errors.New("File paths must be clean relative paths inside the project directory")}
		}
		full := filepath.Join(p.Directory, f)
		e := rejectSymlinks(full)
		if os.IsNotExist(e) && i == len(fs)-1 {
			continue
		}
		if e != nil {
			return &projectFieldError{field, errors.New("A Compose or environment file is missing or inaccessible; verify its path and mount")}
		}
		info, e := os.Stat(full)
		if e != nil || !info.Mode().IsRegular() {
			return &projectFieldError{field, errors.New("Compose and environment paths must be regular files")}
		}
	}
	return nil
}

func revision(p Project) (string, error) {
	if e := validateProjectFiles(p); e != nil {
		return "", e
	}
	h := sha256.New()
	fs := append(append([]string{}, p.ComposeFiles...), p.EnvFiles...)
	fs = append(fs, ".env")
	for _, f := range fs {
		b, e := os.ReadFile(filepath.Join(p.Directory, f))
		if os.IsNotExist(e) && f == ".env" {
			fmt.Fprintf(h, "%s:absent\x00", f)
			continue
		}
		if e != nil {
			return "", e
		}
		fmt.Fprintf(h, "%s:%d\x00", f, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Walk with Lstat so a rejected link is never followed into another directory.
// File revisions recheck this before actions; external editors must still not race operations.
func rejectSymlinks(path string) error {
	at := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), at), string(filepath.Separator)) {
		at = filepath.Join(at, part)
		info, e := os.Lstat(at)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Symlinks are not supported in project directories or files (including .env)")
		}
	}
	return nil
}
