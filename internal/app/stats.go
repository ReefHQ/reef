package app

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"reef/internal/docker"
)

const statsPageSize = 32
const statsCacheSize = 256
const statsTTL = 2 * time.Second

type cachedStats struct {
	value   docker.Stats
	err     error
	checked time.Time
	done    chan struct{}
}
type statsSampler struct {
	mu      sync.Mutex
	slots   chan struct{}
	entries map[string]*cachedStats
}

func newStatsSampler() *statsSampler {
	return &statsSampler{slots: make(chan struct{}, 8), entries: map[string]*cachedStats{}}
}
func (s *statsSampler) read(ctx context.Context, reader docker.StatsReader, id string) (docker.Stats, error) {
	s.mu.Lock()
	if item := s.entries[id]; item != nil {
		if item.done != nil {
			done := item.done
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return docker.Stats{}, ctx.Err()
			case <-done:
				return item.value, item.err
			}
		}
		if time.Since(item.checked) < statsTTL {
			s.mu.Unlock()
			return item.value, item.err
		}
		delete(s.entries, id)
	}
	if len(s.entries) >= statsCacheSize {
		oldest := ""
		var checked time.Time
		for key, item := range s.entries {
			if item.done == nil && (oldest == "" || item.checked.Before(checked)) {
				oldest = key
				checked = item.checked
			}
		}
		if oldest == "" {
			s.mu.Unlock()
			return docker.Stats{}, errors.New("Statistics collection busy")
		}
		delete(s.entries, oldest)
	}
	item := &cachedStats{done: make(chan struct{})}
	done := item.done
	s.entries[id] = item
	s.mu.Unlock()
	var value docker.Stats
	var err error
	select {
	case s.slots <- struct{}{}:
		value, err = reader.ReadStats(ctx, id)
		<-s.slots
	case <-ctx.Done():
		err = ctx.Err()
	}
	s.mu.Lock()
	item.value = value
	item.err = err
	item.checked = time.Now()
	item.done = nil
	// Cancelled samples are not cached, so reopening does not inherit cancellation.
	if ctx.Err() != nil {
		delete(s.entries, id)
	}
	close(done)
	s.mu.Unlock()
	return value, err
}

type containerStats struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Image   string        `json:"image"`
	State   string        `json:"state"`
	Project string        `json:"project"`
	Stats   *docker.Stats `json:"stats"`
	Error   string        `json:"error,omitempty"`
}

func (s *Server) containerStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	offset := 0
	var err error
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 10000 || offset%statsPageSize != 0 {
			http.Error(w, "Invalid statistics page", 400)
			return
		}
	}
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	state := r.URL.Query().Get("state")
	if len(search) > 512 || (state != "" && state != "running" && state != "stopped") {
		http.Error(w, "Invalid statistics filters", 400)
		return
	}
	reader, ok := s.discovery.(docker.StatsReader)
	if !ok {
		http.Error(w, "Container statistics unavailable", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	inventory, err := s.discovery.List(ctx)
	if err != nil || len(inventory) > 10000 {
		http.Error(w, "Docker inventory unavailable; statistics cannot be collected", 503)
		return
	}
	filtered := []docker.Container{}
	seen := map[string]bool{}
	for _, c := range inventory {
		if !docker.ValidContainerID(c.ID) || seen[c.ID] {
			http.Error(w, "Invalid container inventory", 503)
			return
		}
		seen[c.ID] = true
		if state == "running" && c.State != "running" || state == "stopped" && c.State == "running" {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(c.Name+" "+c.Image+" "+c.ComposeProject+" "+c.ID), search) {
			continue
		}
		filtered = append(filtered, c)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Name == filtered[j].Name {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].Name < filtered[j].Name
	})
	total := len(filtered)
	if offset >= total {
		offset = 0
	}
	end := min(offset+statsPageSize, total)
	rows := make([]containerStats, end-offset)
	var jobs sync.WaitGroup
	for i, c := range filtered[offset:end] {
		rows[i] = containerStats{ID: c.ID, Name: c.Name, Image: c.Image, State: c.State, Project: c.ComposeProject}
		if c.State != "running" {
			continue
		}
		jobs.Add(1)
		go func(index int, c docker.Container) {
			defer jobs.Done()
			value, err := s.stats.read(ctx, reader, c.ID)
			if err != nil {
				rows[index].Error = "Statistics unavailable; refresh after checking container and Docker status"
				return
			}
			rows[index].Stats = &value
		}(i, c)
	}
	jobs.Wait()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"containers": rows, "host_total": len(inventory), "total": total, "offset": offset, "limit": statsPageSize, "checked_at": time.Now().UTC()})
}
