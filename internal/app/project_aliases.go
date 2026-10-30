package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type projectAlias struct {
	Alias    string `json:"alias"`
	Target   string `json:"target"`
	Editable bool   `json:"editable"`
}
type aliasSuggestion struct {
	Alias     string `json:"alias"`
	Target    string `json:"target"`
	Directory string `json:"directory"`
}
type pathAliasState struct {
	Version int               `json:"version"`
	Aliases map[string]string `json:"aliases"`
}

func readPathAliases(dir string) ([]byte, error) {
	path := filepath.Join(dir, "project-path-aliases.json")
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 64<<10 {
		return nil, errors.New("Invalid private project alias state")
	}
	return os.ReadFile(path)
}
func (m *Manager) aliasConfigLocked() Config {
	c := m.config
	c.ProjectPathAliases = map[string]string{}
	for a, d := range m.config.ProjectPathAliases {
		c.ProjectPathAliases[a] = d
	}
	for a, d := range m.pathAliases {
		c.ProjectPathAliases[a] = d
	}
	return c
}
func (m *Manager) aliasConfig() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.aliasConfigLocked()
}
func (m *Manager) loadPathAliases() error {
	raw, e := readPathAliases(m.StateDir)
	if e != nil {
		return e
	}
	m.pathAliases = map[string]string{}
	if len(raw) > 0 {
		var state pathAliasState
		if json.Unmarshal(raw, &state) != nil || state.Version != 1 || state.Aliases == nil {
			return errors.New("Invalid project alias state; refusing to discard saved mappings")
		}
		for a, d := range state.Aliases {
			if _, exists := m.config.ProjectPathAliases[a]; exists {
				return errors.New("Project alias conflicts with configuration; reconcile saved and configured mappings")
			}
			m.pathAliases[a] = d
		}
	}
	if e = validateProjectPathAliases(m.aliasConfigLocked()); e != nil {
		return e
	}
	m.pathAliasHash = digest(raw)
	return nil
}
func (m *Manager) aliasList() []projectAlias {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := []projectAlias{}
	for a, d := range m.config.ProjectPathAliases {
		rows = append(rows, projectAlias{a, d, false})
	}
	for a, d := range m.pathAliases {
		rows = append(rows, projectAlias{a, d, true})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Alias < rows[j].Alias })
	return rows
}
func (m *Manager) savePathAlias(action, alias, target, requested string, p Project) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.agentUpdating {
		return "", errors.New("Wait for the agent update")
	}
	if _, exists := m.config.ProjectPathAliases[alias]; exists {
		return "", errors.New("This alias is defined in configuration and cannot be changed here")
	}
	next := map[string]string{}
	for a, d := range m.pathAliases {
		next[a] = d
	}
	c := m.aliasConfigLocked()
	resolved := ""
	switch action {
	case "save":
		// UI mappings are created explicitly, never silently replaced.
		if _, exists := next[alias]; exists {
			return "", errors.New("Alias already saved; remove it before replacing it")
		}
		c.ProjectPathAliases[alias] = target
		if e := validateProjectPathAliases(c); e != nil {
			return "", e
		}
		if !filepath.IsAbs(requested) || filepath.Clean(requested) != requested || !within(alias, requested) {
			return "", errors.New("The project folder must be inside the alias being confirmed")
		}
		var e error
		resolved, e = resolveProjectDirectory(c, requested)
		if e != nil {
			return "", e
		}
		rel, _ := filepath.Rel(alias, requested)
		if resolved != filepath.Join(target, rel) {
			return "", errors.New("The visible alias does not match the proposed destination")
		}
		p.Directory = resolved
		if e = validateProjectFiles(p); e != nil {
			return "", e
		}
		next[alias] = target
	case "remove":
		if _, exists := next[alias]; !exists {
			return "", errors.New("Saved alias no longer exists")
		}
		delete(next, alias)
	default:
		return "", errors.New("Choose save or remove for a project alias")
	}
	raw, e := readPathAliases(m.StateDir)
	if e != nil {
		return "", e
	}
	if digest(raw) != m.pathAliasHash {
		return "", errors.New("Project aliases changed outside Reef; restart before editing them")
	}
	b, e := json.Marshal(pathAliasState{1, next})
	if e != nil {
		return "", e
	}
	if len(b) > 64<<10 {
		return "", errors.New("Project alias state is too large")
	}
	if e = atomicWrite(filepath.Join(m.StateDir, "project-path-aliases.json"), b, 0600); e != nil {
		return "", errors.New("Could not confirm durable alias storage; check saved state and restart before retrying")
	}
	m.pathAliases = next
	m.pathAliasHash = digest(b)
	return resolved, nil
}
func (s *Server) projectPathAliases(w http.ResponseWriter, r *http.Request) {
	if r.Form.Get("confirmed") != "yes" {
		http.Error(w, "Explicit confirmation of the alias correspondence is required", 400)
		return
	}
	p := Project{ComposeFiles: formLines(r.Form.Get("compose_files")), EnvFiles: formLines(r.Form.Get("env_files"))}
	if r.Form.Get("action") == "save" {
		if _, e := s.discovered(r, r.Form.Get("project_name")); e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		if len(p.ComposeFiles) > 32 || len(p.EnvFiles) > 32 {
			http.Error(w, "At most 32 Compose and environment files are supported", 400)
			return
		}
	}
	dir, e := s.m.savePathAlias(r.Form.Get("action"), r.Form.Get("alias"), r.Form.Get("target"), r.Form.Get("directory"), p)
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	writeJSON(w, map[string]any{"resolved_directory": dir, "path_aliases": s.m.aliasList()})
}

// These are reviewable hypotheses, never proof of alias equivalence. Require a
// shared multi-component suffix, an existing authorized destination and regular
// project files. Never scan whole roots or activate a suggestion automatically.
func suggestProjectAliases(c Config, dir string, files []string) []aliasSuggestion {
	result := []aliasSuggestion{}
	if len(dir) > 4096 || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || len(files) == 0 || len(files) > 32 {
		return result
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		return result
	}
	parts := strings.Split(strings.TrimPrefix(dir, "/"), "/")
	if len(parts) > 64 {
		return result
	}
	seen := map[string]bool{}
	roots := allowedRoots(c)
	if len(roots) > 64 {
		roots = roots[:64]
	}
rootsLoop:
	for _, root := range roots {
		if len(root) > 4096 {
			continue
		}
		targetParts := strings.Split(strings.TrimPrefix(root, "/"), "/")
		if len(targetParts) > 64 {
			continue
		}
		// Try longest matching root suffix first; never match a lone folder name.
		for n := len(targetParts); n >= 2; n-- {
			suffix := strings.Join(targetParts[len(targetParts)-n:], "/")
			for i := 0; i+n < len(parts); i++ {
				if strings.Join(parts[i:i+n], "/") != suffix {
					continue
				}
				alias := "/" + strings.Join(parts[:i+n], "/")
				if _, exists := c.ProjectPathAliases[alias]; exists {
					continue rootsLoop
				}
				key := alias + "\x00" + root
				if seen[key] {
					continue rootsLoop
				}
				seen[key] = true
				copy := c
				copy.ProjectPathAliases = map[string]string{}
				for a, d := range c.ProjectPathAliases {
					copy.ProjectPathAliases[a] = d
				}
				copy.ProjectPathAliases[alias] = root
				if validateProjectPathAliases(copy) != nil {
					continue rootsLoop
				}
				physical, e := resolveProjectDirectory(copy, dir)
				if e != nil {
					continue rootsLoop
				}
				if validateProjectFiles(Project{Directory: physical, ComposeFiles: files}) != nil {
					continue rootsLoop
				}
				result = append(result, aliasSuggestion{alias, root, physical})
				if len(result) == 16 {
					return result
				}
				continue rootsLoop
			}
		}
	}
	return result
}
