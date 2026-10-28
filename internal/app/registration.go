package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"reef/internal/docker"
)

type registry struct {
	Version  int       `json:"version"`
	Projects []Project `json:"projects"`
}

func within(root, path string) bool {
	rel, e := filepath.Rel(root, path)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func validateRoots(roots []string) error {
	for _, r := range roots {
		if !filepath.IsAbs(r) || filepath.Clean(r) != r || r == string(filepath.Separator) {
			return errors.New("allowed_project_roots must contain canonical absolute directories, never the filesystem root")
		}
	}
	return nil
}

// The installation is the canonical host folder containing Reef's launcher/source.
// Never infer this path from cwd, candidate projects or Docker labels.
// A non-nil explicit list (including []) always takes precedence.
func allowedRoots(c Config) []string {
	if c.AllowedProjectRoots != nil {
		return append([]string{}, c.AllowedProjectRoots...)
	}
	dir := c.InstallationDirectory
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == string(filepath.Separator) || filepath.Dir(dir) == string(filepath.Separator) {
		return []string{}
	}
	return []string{filepath.Dir(dir)}
}

type rootPolicy struct {
	Roots                 []string `json:"roots"`
	Source                string   `json:"source"`
	InstallationDirectory string   `json:"installation_directory,omitempty"`
}

func projectAccess(c Config) rootPolicy {
	source := "installation-parent"
	if c.AllowedProjectRoots != nil {
		source = "explicit"
	} else if len(allowedRoots(c)) == 0 {
		source = "installation-unknown"
	}
	return rootPolicy{allowedRoots(c), source, c.InstallationDirectory}
}
func authorizedDirectory(c Config, dir string) error {
	if c.InstallationDirectory != "" && filepath.Clean(dir) == c.InstallationDirectory {
		return errors.New("Reef cannot manage its own installation directory")
	}
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return errors.New("Use a canonical absolute project directory")
	}
	for _, root := range allowedRoots(c) {
		if within(root, dir) {
			actual, e := filepath.EvalSymlinks(root)
			if e != nil || actual != root {
				return errors.New("Authorized root is unavailable or noncanonical. Mount it into Reef at the identical absolute host path")
			}
			return nil
		}
	}
	return errors.New("Directory is outside Reef's authorized project roots. An administrator must explicitly authorize and mount a parent at the identical absolute host path; this form cannot add mounts")
}
func registryBytes(c Config) ([]byte, error) {
	path := filepath.Join(c.StateDir, "registered-projects.json")
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("Invalid project registry file")
	}
	return os.ReadFile(path)
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func readRegistrations(c Config) ([]Project, error) {
	b, e := registryBytes(c)
	if e != nil || b == nil {
		return nil, e
	}
	var data registry
	if e = json.Unmarshal(b, &data); e != nil || data.Version != 1 {
		return nil, errors.New("Invalid project registry; refusing to discard registered projects")
	}
	for _, p := range data.Projects {
		if e = authorizedDirectory(c, p.Directory); e != nil {
			return nil, e
		}
	}
	if e = validateProjects(append(append([]Project{}, c.Projects...), data.Projects...)); e != nil {
		return nil, fmt.Errorf("Cannot load project registry: %w", e)
	}
	return data.Projects, nil
}
func (m *Manager) saveRegistrations(next []Project) error {
	// m.mu serializes concurrent registrations/removals. Refuse external edits too.
	old, e := registryBytes(m.config)
	if e != nil {
		return e
	}
	if digest(old) != m.registryHash {
		return errors.New("Project registry changed outside Reef; restart before registering projects")
	}
	b, e := json.MarshalIndent(registry{1, next}, "", "  ")
	if e != nil {
		return e
	}
	if len(b) > 1<<20 {
		return errors.New("Project registry limit reached")
	}
	if e = atomicWrite(filepath.Join(m.StateDir, "registered-projects.json"), b, 0600); e != nil {
		return fmt.Errorf("Cannot persist project registration: %w", e)
	}
	m.registryHash = digest(b)
	m.registered = next
	return nil
}
func (m *Manager) registerProject(p Project) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.agentUpdating {
		return p, errors.New("Wait for the agent update")
	}
	if e := authorizedDirectory(m.config, p.Directory); e != nil {
		return p, &projectFieldError{"directory", e}
	}
	if !identifier.MatchString(p.ProjectName) || len(p.ProjectName) > 128 || strings.TrimSpace(p.Name) == "" || len(p.Name) > 200 {
		return p, &projectFieldError{"name", errors.New("A valid Compose project identity and display name are required")}
	}
	if len(p.ComposeFiles) > 32 || len(p.EnvFiles) > 32 {
		return p, errors.New("At most 32 Compose and 32 environment files are supported")
	}
	// Check conflicts before any file access to the submitted project.
	for _, existing := range m.Projects {
		if existing.Project.ProjectName == p.ProjectName {
			return p, errors.New("This Compose project is already managed")
		}
		for _, f := range p.ComposeFiles {
			for _, other := range existing.Project.ComposeFiles {
				if filepath.Join(p.Directory, f) == filepath.Join(existing.Project.Directory, other) {
					return p, &projectFieldError{"compose_files", errors.New("Compose files cannot be shared between registered projects")}
				}
			}
		}
	}
	if e := validateProjectFiles(p); e != nil {
		return p, e
	}
	// Check read/write access without changing any project file or creating directories.
	for _, f := range append(append([]string{}, p.ComposeFiles...), p.EnvFiles...) {
		h, e := os.Open(filepath.Join(p.Directory, f))
		if e != nil {
			return p, errors.New("A Compose or environment file is not readable")
		}
		h.Close()
	}
	main := filepath.Join(p.Directory, p.ComposeFiles[0])
	h, e := os.OpenFile(main, os.O_RDWR, 0)
	if e != nil {
		return p, &projectFieldError{"directory", errors.New("Main Compose file is not writable; check Reef's bind mount and permissions")}
	}
	h.Close()
	if e = syscall.Access(filepath.Dir(main), 2); e != nil {
		return p, &projectFieldError{"directory", errors.New("Main Compose directory is not writable; a read/write mount is required for atomic saves")}
	}
	p.ID = "ui-" + randomID()[:16]
	next := append(append([]Project{}, m.registered...), p)
	if e = m.saveRegistrations(next); e != nil {
		return p, e
	}
	m.Projects[p.ID] = &Managed{Project: p}
	m.Order = append(m.Order, p.ID)
	m.projectEvent(p, "register", "completed", "")
	return p, nil
}
func (m *Manager) unregisterProject(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.agentUpdating {
		return errors.New("Wait for the agent update")
	}
	index := -1
	for i, p := range m.registered {
		if p.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("Only projects registered through the UI can be removed here")
	}
	p := m.Projects[id]
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy || p.ImageUpdates.Checking {
		return errors.New("Wait for the active project operation to finish")
	}
	next := append([]Project{}, m.registered[:index]...)
	next = append(next, m.registered[index+1:]...)
	if e := m.saveRegistrations(next); e != nil {
		return e
	}
	m.projectEvent(p.Project, "unmanage", "completed", "")
	delete(m.Projects, id)
	m.Order = slices.DeleteFunc(m.Order, func(s string) bool { return s == id })
	return nil
}
func (m *Manager) registeredID(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.registered {
		if p.ProjectName == name {
			return p.ID
		}
	}
	return ""
}
func selfProject(rows []docker.Container, name string) bool {
	host, _ := os.Hostname()
	for _, c := range rows {
		if c.ComposeProject == name && (c.Reef || (len(host) >= 12 && strings.HasPrefix(c.ID, host))) {
			return true
		}
	}
	return false
}
func (s *Server) discovered(r *http.Request, name string) ([]docker.Container, error) {
	if !identifier.MatchString(name) || len(name) > 128 {
		return nil, errors.New("Invalid Compose project name")
	}
	rows, e := s.discovery.List(r.Context())
	if e != nil {
		return nil, e
	}
	if selfProject(rows, name) {
		return nil, errors.New("Reef cannot manage its own Compose project")
	}
	found := []docker.Container{}
	for _, c := range rows {
		if c.ComposeProject == name {
			found = append(found, c)
		}
	}
	if len(found) == 0 {
		return nil, errors.New("This Compose project is no longer present on Docker; refresh the inventory")
	}
	return found, nil
}
func (s *Server) proposal(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	rows, e := s.discovered(r, name)
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	// No filesystem calls: labels may refer to another machine, a missing mount, or hostile paths.
	dir, files := rows[0].WorkingDir, rows[0].ConfigFiles
	for _, c := range rows[1:] {
		if c.WorkingDir != dir {
			dir = ""
		}
		if c.ConfigFiles != files {
			files = ""
		}
	}
	paths := []string{}
	if files != "" {
		paths = strings.Split(files, ",")
	}
	for i, f := range paths {
		if filepath.IsAbs(f) && filepath.IsAbs(dir) && within(dir, f) {
			if rel, e := filepath.Rel(dir, f); e == nil {
				paths[i] = rel
			}
		}
	}
	writeJSON(w, map[string]any{"project_name": name, "directory": dir, "compose_files": paths, "allowed_roots": allowedRoots(s.cfg), "project_access": projectAccess(s.cfg), "notice": "Docker labels are untrusted suggestions. Confirm the exact existing project identity, directory, ordered Compose files and environment options. Custom env files, profiles and original CLI options cannot be reliably recovered. Registration only records access; it does not run Compose or validate Compose syntax."})
}
func formLines(s string) []string {
	rows := []string{}
	for _, v := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if v = strings.TrimSpace(v); v != "" {
			rows = append(rows, v)
		}
	}
	return rows
}
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if r.Form.Get("confirmed") != "yes" {
		http.Error(w, "Explicit confirmation of the project paths and environment options is required", 400)
		return
	}
	name := r.Form.Get("project_name")
	if _, e := s.discovered(r, name); e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	p, e := s.m.registerProject(Project{Name: strings.TrimSpace(r.Form.Get("name")), ProjectName: name, Directory: r.Form.Get("directory"), ComposeFiles: formLines(r.Form.Get("compose_files")), EnvFiles: formLines(r.Form.Get("env_files"))})
	if e != nil {
		var field *projectFieldError
		if errors.As(e, &field) {
			w.Header().Set("X-Reef-Field", field.Field)
		}
		http.Error(w, e.Error(), 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, p)
}
func (s *Server) unmanage(w http.ResponseWriter, r *http.Request) {
	if r.Form.Get("confirmed") != "yes" {
		http.Error(w, "Confirmation required", 400)
		return
	}
	if e := s.m.unregisterProject(r.Form.Get("id")); e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	w.WriteHeader(204)
}

type projectFieldError struct {
	Field string
	Err   error
}

func (e *projectFieldError) Error() string { return e.Err.Error() }
func (e *projectFieldError) Unwrap() error { return e.Err }
