package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type BuildOptions struct {
	Service string `json:"service,omitempty"`
	NoCache bool   `json:"no_cache,omitempty"`
	Pull    bool   `json:"pull,omitempty"`
}

var buildServicePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
var buildOperationPattern = regexp.MustCompile(`^[a-f0-9]{48}$`)

func (m *Manager) buildServices(ctx context.Context, p Project, rev string) ([]string, error) {
	if err := sameRevision(p, rev); err != nil {
		return nil, err
	}
	if m.Images == nil {
		return nil, errors.New("Docker build inspection unavailable")
	}
	raw, err := m.Images.command(ctx, p, composeArgs(p, "", "config", "--format", "json")...)
	if err != nil {
		return nil, errors.New("Cannot read saved Compose build configuration")
	}
	var config struct {
		Services map[string]struct{ Build json.RawMessage }
	}
	if json.Unmarshal(raw, &config) != nil || len(config.Services) > 1000 {
		return nil, errors.New("Invalid or oversized Compose build configuration")
	}
	services := []string{}
	for name, service := range config.Services {
		if len(service.Build) > 0 && string(service.Build) != "null" {
			if !buildServicePattern.MatchString(name) {
				return nil, errors.New("Unsupported build service name")
			}
			services = append(services, name)
		}
	}
	if len(services) > 100 {
		return nil, errors.New("A project supports at most 100 build services")
	}
	sort.Strings(services)
	if err := sameRevision(p, rev); err != nil {
		return nil, err
	}
	return services, nil
}
func (m *Manager) runBuild(ctx context.Context, p *Managed, w *logWriter, rev string) error {
	services, err := m.buildServices(ctx, p.Project, rev)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return errors.New("No services with a build context in the saved Compose files")
	}
	opts := p.Op.Build
	if opts.Service != "" {
		found := false
		for _, name := range services {
			found = found || name == opts.Service
		}
		if !found {
			return errors.New("Selected service has no saved build context")
		}
		services = []string{opts.Service}
	}
	args := []string{"build", "--builder", "default"}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	if opts.Pull {
		args = append(args, "--pull")
	}
	args = append(args, "--")
	args = append(args, services...)
	if err = m.run(ctx, p, w, "", args...); err != nil {
		return err
	}
	return sameRevision(p.Project, rev)
}
func buildSummary(op Operation) Operation { op.Log = ""; return op }
func (m *Manager) buildHistory(p *Managed) ([]Operation, error) {
	p.mu.Lock()
	id := p.Project.ID
	current := p.Op
	p.mu.Unlock()
	directory, err := os.Open(m.StateDir)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(10001)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > 10000 {
		return nil, errors.New("History directory exceeds 10000 entries; archive older history files")
	}
	type entry struct {
		name     string
		modified time.Time
	}
	files := []entry{}
	for _, file := range entries {
		prefix := id + "-"
		if !strings.HasPrefix(file.Name(), prefix) || !strings.HasSuffix(file.Name(), ".build.json") {
			continue
		}
		token := strings.TrimSuffix(strings.TrimPrefix(file.Name(), prefix), ".build.json")
		if !buildOperationPattern.MatchString(token) || file.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, e := file.Info()
		if e != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, entry{file.Name(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modified.After(files[j].modified) })
	rows := []Operation{}
	seen := map[string]bool{}
	if current.Action == "build" && current.ID != "" {
		rows = append(rows, buildSummary(current))
		seen[current.ID] = true
	}
	if len(files) > 50 {
		files = files[:50]
	}
	for _, file := range files {
		op, e := m.readBuildSummary(id, file.name)
		if e != nil {
			return nil, e
		}
		if !seen[op.ID] {
			rows = append(rows, op)
			seen[op.ID] = true
		}
		if len(rows) >= 50 {
			break
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Started.After(rows[j].Started) })
	return rows, nil
}
func (m *Manager) writeBuildSummary(id string, op Operation) error {
	raw, _ := json.Marshal(buildSummary(op))
	return atomicWrite(filepath.Join(m.StateDir, id+"-"+op.ID+".build.json"), raw, 0600)
}
func (m *Manager) readBuildSummary(id, name string) (Operation, error) {
	var op Operation
	path := filepath.Join(m.StateDir, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 128<<10 {
		return op, errors.New("Build history unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 128<<10 || json.Unmarshal(raw, &op) != nil || op.Action != "build" || !buildOperationPattern.MatchString(op.ID) || name != id+"-"+op.ID+".build.json" {
		return Operation{}, errors.New("Invalid build history")
	}
	return buildSummary(op), nil
}
func (m *Manager) readBuildHistory(id, name string) (Operation, error) {
	var op Operation
	path := filepath.Join(m.StateDir, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return op, errors.New("Build history unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 2<<20 || json.Unmarshal(raw, &op) != nil || !buildOperationPattern.MatchString(op.ID) || name != id+"-"+op.ID+".history.json" {
		return Operation{}, errors.New("Invalid build history")
	}
	return op, nil
}
func (s *Server) builds(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if r.URL.Path == "/api/builds" {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		rows := []map[string]any{}
		for _, p := range s.m.snapshot() {
			p.mu.Lock()
			op := Operation{}
			if p.Op.Action == "build" {
				op = buildSummary(p.Op)
			}
			rows = append(rows, map[string]any{"id": p.Project.ID, "name": p.Project.Name, "busy": p.busy, "persistence_error": p.persistError, "operation": op})
			p.mu.Unlock()
		}
		writeJSON(w, rows)
		return
	}
	if r.URL.Path == "/api/builds/cache" || r.URL.Path == "/api/builds/cache/action" || r.URL.Path == "/api/builds/cache/operation" {
		s.buildCacheAPI(w, r)
		return
	}
	id := r.URL.Query().Get("id")
	if r.Method == "POST" {
		id = r.Form.Get("id")
	}
	p, ok := s.m.get(id)
	if !ok {
		http.Error(w, "Unknown managed project", 404)
		return
	}
	switch r.URL.Path {
	case "/api/builds/project":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.busy {
			http.Error(w, "Project operation in progress", 409)
			return
		}
		rev, err := revision(p.Project)
		if err != nil {
			http.Error(w, "Cannot read saved project", 409)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		services, err := s.m.buildServices(ctx, p.Project, rev)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, map[string]any{"revision": rev, "services": services})
	case "/api/builds/history":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		rows, err := s.m.buildHistory(p)
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		writeJSON(w, rows)
	case "/api/builds/log":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		token := r.URL.Query().Get("operation")
		if !buildOperationPattern.MatchString(token) {
			http.Error(w, "Invalid build identity", 400)
			return
		}
		p.mu.Lock()
		op := p.Op
		p.mu.Unlock()
		if op.ID != token {
			var err error
			op, err = s.m.readBuildHistory(id, id+"-"+token+".history.json")
			if err != nil {
				http.Error(w, "Build log unavailable", 404)
				return
			}
		}
		if op.Action != "build" {
			http.Error(w, "Build log unavailable", 404)
			return
		}
		writeJSON(w, op)
	case "/api/builds/action":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if r.Form.Get("confirmed") != "yes" {
			http.Error(w, "Build confirmation required", 400)
			return
		}
		opts := BuildOptions{Service: r.Form.Get("service"), NoCache: r.Form.Get("no_cache") == "yes", Pull: r.Form.Get("pull") == "yes"}
		if opts.Service != "" && !buildServicePattern.MatchString(opts.Service) {
			http.Error(w, "Invalid service", 400)
			return
		}
		if err := s.m.beginOptions(id, "build", r.Form.Get("revision"), "", opts); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.WriteHeader(202)
	default:
		http.NotFound(w, r)
	}
}
