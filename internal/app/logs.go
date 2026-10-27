package app

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"reef/internal/docker"
)

func (s *Server) containerLogs(w http.ResponseWriter, r *http.Request) {
	id, stream := r.URL.Query().Get("id"), r.URL.Query().Get("stream")
	if stream == "" {
		stream = "all"
	}
	tail := 200
	if value := r.URL.Query().Get("tail"); value != "" {
		var e error
		tail, e = strconv.Atoi(value)
		if e != nil {
			http.Error(w, "Invalid log options.", 400)
			return
		}
	}
	if !docker.ValidContainerID(id) || (stream != "all" && stream != "stdout" && stream != "stderr") || tail < 1 || tail > 1000 {
		http.Error(w, "Invalid log options.", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	discovery, e := s.discoveryFor(r)
	if e != nil {
		http.Error(w, e.Error(), 503)
		return
	}
	rows, e := discovery.List(ctx)
	if e != nil {
		http.Error(w, "Docker inventory is unavailable. Cannot read container logs.", 503)
		return
	}
	var found *docker.Container
	for i := range rows {
		if rows[i].ID == id {
			found = &rows[i]
			break
		}
	}
	if found == nil {
		http.Error(w, "Container not found. Refresh the container list.", 404)
		return
	}
	reader, ok := discovery.(docker.LogReader)
	if !ok {
		http.Error(w, "Container logs are unavailable.", 503)
		return
	}
	logs, e := reader.ReadLogs(ctx, id, stream, tail)
	if e != nil {
		http.Error(w, e.Error(), 503)
		return
	}
	writeJSON(w, struct {
		docker.Logs
		ContainerID string    `json:"container_id"`
		Name        string    `json:"name"`
		CheckedAt   time.Time `json:"checked_at"`
	}{logs, found.ID, found.Name, time.Now()})
}
