package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reef/internal/docker"
	"regexp"
	"strconv"
	"time"
)

type BuildCacheRecord struct {
	ID          string `json:"id"`
	Created     string `json:"created"`
	LastUsed    string `json:"last_used"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	UsageCount  int64  `json:"usage_count"`
	Shared      bool   `json:"shared"`
	Mutable     bool   `json:"mutable"`
	Reclaimable bool   `json:"reclaimable"`
	Fingerprint string `json:"fingerprint"`
}
type CacheTarget struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
}

var buildCacheIDPattern = regexp.MustCompile(`^[a-z0-9]{8,128}$`)

func (d DockerExecutor) RawBuildCache(ctx context.Context) ([]docker.BuildCacheRecord, error) {
	socket := d.Socket
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	return docker.NewClient(socket).BuildCache(ctx)
}
func (s *ImageService) cacheRecords(ctx context.Context, id string) ([]BuildCacheRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"buildx", "du", "--builder", "default", "--format=json"}
	if id != "" {
		args = append(args, "--filter", "id="+id)
	}
	raw, err := s.command(ctx, Project{}, args...)
	if err != nil {
		return nil, errors.New("Default builder cache unavailable; check Docker Buildx on this host")
	}
	engine := map[string]docker.BuildCacheRecord{}
	if reader, ok := s.Exec.(interface {
		RawBuildCache(context.Context) ([]docker.BuildCacheRecord, error)
	}); ok {
		records, e := reader.RawBuildCache(ctx)
		if e != nil {
			return nil, e
		}
		for _, record := range records {
			engine[record.ID] = record
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	rows := []BuildCacheRecord{}
	seen := map[string]bool{}
	for {
		var item struct {
			ID, CreatedAt, LastUsedAt, Type string
			Size                            json.RawMessage
			UsageCount                      int64
			Shared, Mutable, Reclaimable    bool
		}
		err := decoder.Decode(&item)
		if err == io.EOF {
			break
		}
		var sizeValue string
		var size int64
		var e error
		if source, ok := engine[item.ID]; ok {
			size = source.Size
			item.CreatedAt = source.CreatedAt
			item.LastUsedAt = source.LastUsedAt
			item.UsageCount = source.UsageCount
			item.Shared = source.Shared
			item.Reclaimable = item.Reclaimable && !source.InUse
		} else {
			if len(engine) > 0 {
				return nil, errors.New("Cache changed during inspection; refresh before retrying")
			}
			if json.Unmarshal(item.Size, &sizeValue) != nil {
				sizeValue = string(item.Size)
			}
			size, e = strconv.ParseInt(sizeValue, 10, 64)
		}
		if err != nil || e != nil || size < 0 || item.UsageCount < 0 || !buildCacheIDPattern.MatchString(item.ID) || seen[item.ID] || (id != "" && id != item.ID) {
			return nil, errors.New("Invalid builder cache metadata")
		}
		seen[item.ID] = true
		row := BuildCacheRecord{ID: item.ID, Created: item.CreatedAt, LastUsed: item.LastUsedAt, Type: item.Type, Size: size, UsageCount: item.UsageCount, Shared: item.Shared, Mutable: item.Mutable, Reclaimable: item.Reclaimable}
		fingerprint, _ := json.Marshal(row)
		row.Fingerprint = digest(fingerprint)
		rows = append(rows, row)
		if len(rows) > 10000 {
			return nil, errors.New("Builder cache inventory exceeds 10000 records")
		}
	}
	return rows, nil
}
func (s *ImageService) cacheOperation() CatalogOperation {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	op := s.cacheOp
	op.Results = append([]ImageResult{}, op.Results...)
	return op
}
func (s *Server) buildCacheAPI(w http.ResponseWriter, r *http.Request) {
	if s.m.Images == nil {
		http.Error(w, "Builder cache unavailable", 503)
		return
	}
	switch r.URL.Path {
	case "/api/builds/cache":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		rows, err := s.m.Images.cacheRecords(r.Context(), "")
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		writeJSON(w, map[string]any{"records": rows, "operation": s.m.Images.cacheOperation(), "builder": "default"})
	case "/api/builds/cache/operation":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		writeJSON(w, s.m.Images.cacheOperation())
	case "/api/builds/cache/action":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if r.Form.Get("confirmed") != "yes" {
			http.Error(w, "Cache cleanup confirmation required", 400)
			return
		}
		var targets []CacheTarget
		if json.Unmarshal([]byte(r.Form.Get("targets")), &targets) != nil || len(targets) == 0 || len(targets) > 10000 {
			http.Error(w, "Select 1–10000 cache records", 400)
			return
		}
		seen := map[string]bool{}
		for _, target := range targets {
			if !buildCacheIDPattern.MatchString(target.ID) || !containerIDPattern.MatchString(target.Fingerprint) || seen[target.ID] {
				http.Error(w, "Invalid cache selection", 400)
				return
			}
			seen[target.ID] = true
		}
		op, err := s.m.beginCacheCleanup(targets)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		writeJSON(w, op)
	}
}
func (m *Manager) beginCacheCleanup(targets []CacheTarget) (CatalogOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storageBusy {
		return CatalogOperation{}, errors.New("Wait for image, volume or build operations")
	}
	for _, p := range m.Projects {
		p.mu.Lock()
		busy := p.busy || p.ImageUpdates.Checking
		p.mu.Unlock()
		if busy {
			return CatalogOperation{}, errors.New("Wait for project operations and checks")
		}
	}
	s := m.Images
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	op := CatalogOperation{ID: randomID(), Action: "cache cleanup", Running: true, Total: len(targets), Started: time.Now(), Results: []ImageResult{}}
	s.cacheOp = op
	m.storageBusy = true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		for _, target := range targets {
			row := ImageResult{ID: target.ID, Message: "Cache removed or already unavailable"}
			if ctx.Err() != nil {
				row.Unprocessed = true
				row.Message = "Not processed: cleanup timed out"
			} else {
				err := s.removeCacheRecord(ctx, target)
				row.OK = err == nil
				if err != nil {
					row.Message = err.Error()
				}
			}
			s.catalogMu.Lock()
			s.cacheOp.Results = append(s.cacheOp.Results, row)
			if row.Unprocessed {
				s.cacheOp.Skipped++
			} else {
				s.cacheOp.Completed++
			}
			s.catalogMu.Unlock()
		}
		m.mu.Lock()
		s.catalogMu.Lock()
		s.cacheOp.Running = false
		s.cacheOp.Finished = time.Now()
		m.storageBusy = false
		s.catalogMu.Unlock()
		m.mu.Unlock()
	}()
	return op, nil
}
func (s *ImageService) removeCacheRecord(ctx context.Context, target CacheTarget) error {
	rows, err := s.cacheRecords(ctx, target.ID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	row := rows[0]
	if row.Fingerprint != target.Fingerprint || !row.Reclaimable || row.Mutable {
		return errors.New("Cache retained: record changed, is mutable or is in use")
	}
	// Fixed default builder and exact ID after checking immutable/reclaimable metadata.
	// Docker retains active cache; force suppresses
	// Docker's interactive prompt only; the browser already confirmed this set.
	if _, err = s.command(ctx, Project{}, "buildx", "prune", "--builder", "default", "--force", "--all", "--filter", "id="+target.ID); err != nil {
		return errors.New("Docker refused targeted cache cleanup")
	}
	remaining, err := s.cacheRecords(ctx, target.ID)
	if err != nil {
		return errors.New("Cleanup outcome unknown; refresh cache before retrying")
	}
	if len(remaining) > 0 {
		return errors.New("Cache retained by Docker; refresh eligibility")
	}
	return nil
}
