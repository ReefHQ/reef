package app

import (
	"net/http"
	"time"

	"reef/internal/docker"
)

func (s *Server) containers(w http.ResponseWriter, r *http.Request) {
	rows, e := s.discovery.List(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 503)
		return
	}
	type item struct {
		docker.Container
		ManagedProjectID string `json:"managed_project_id,omitempty"`
		RegisteredID     string `json:"registered_id,omitempty"`
		IsReef           bool   `json:"is_reef,omitempty"`
	}
	result := make([]item, 0, len(rows))
	names := map[string]string{}
	for _, p := range s.m.snapshot() {
		names[p.Project.ProjectName] = p.Project.ID
	}
	for _, row := range rows {
		result = append(result, item{row, names[row.ComposeProject], s.m.registeredID(row.ComposeProject), selfProject(rows, row.ComposeProject)})
	}
	writeJSON(w, struct {
		Containers    []item     `json:"containers"`
		CheckedAt     time.Time  `json:"checked_at"`
		ProjectAccess rootPolicy `json:"project_access"`
	}{result, time.Now(), projectAccess(s.cfg)})
}
