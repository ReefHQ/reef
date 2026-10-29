package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"reef/internal/agents"
	"reef/internal/agentupdate"
	"reef/internal/buildinfo"
	"reef/internal/notifications"
)

func (s *Server) agentProtocol(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.AgentsEnabled || s.agents == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	// Machine credentials are independent of browser sessions and CSRF forms.
	if r.Header.Get("Origin") != "" || len(r.Cookies()) > 0 {
		http.Error(w, "Browser requests are not accepted", 403)
		return
	}
	if r.URL.Path != "/agent/v1/enroll" && !s.agents.Authenticate(r.Header.Get("X-Reef-Host"), bearer(r)) {
		http.Error(w, "Agent authentication rejected", 403)
		return
	}
	if r.URL.Path == "/agent/v1/events" {
		s.agentEvents(w, r)
		return
	}
	if r.URL.Path == "/agent/v1/commands" || r.URL.Path == "/agent/v1/results" {
		s.agentCommands(w, r)
		return
	}
	limit := int64(4096)
	if r.URL.Path == "/agent/v1/inventory" {
		limit = agents.MaxPayload
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	var input struct {
		Token        string   `json:"token"`
		Protocol     int      `json:"protocol"`
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities,omitempty"`
	}
	// Decode fields explicitly: Update's protocol is used for snapshot requests.
	if r.URL.Path == "/agent/v1/inventory" {
		var u agents.Update
		if decodeAgent(r, &u) != nil {
			http.Error(w, "Invalid inventory payload", 400)
			return
		}
		if e := s.agents.Publish(r.Header.Get("X-Reef-Host"), bearer(r), u); e != nil {
			http.Error(w, e.Error(), 403)
			return
		}
		if s.agents.CanMonitoring(r.Header.Get("X-Reef-Host")) {
			writeJSON(w, map[string]bool{"monitoring_enabled": s.monitor != nil && s.monitor.Enabled()})
		} else {
			w.WriteHeader(204)
		}
		return
	}
	if decodeAgent(r, &input) != nil {
		http.Error(w, "Invalid agent request", 400)
		return
	}
	switch r.URL.Path {
	case "/agent/v1/enroll":
		id, key, e := s.agents.Enroll(input.Token, input.Protocol)
		if e != nil {
			http.Error(w, "Enrollment rejected", 403)
			return
		}
		if view, _, ok := s.agents.Inventory(id); ok {
			s.m.emit(notifications.New("agent", "enroll", "completed", "", view.Name))
		}
		writeJSON(w, map[string]string{"id": id, "credential": key})
	case "/agent/v1/connect":
		generation, e := s.agents.Begin(r.Header.Get("X-Reef-Host"), bearer(r), input.Version, input.Protocol)
		if e != nil {
			http.Error(w, e.Error(), 403)
			return
		}
		enabled := false
		for _, capability := range input.Capabilities {
			if capability == agents.ControlCapability {
				enabled = true
			}
		}
		host := r.Header.Get("X-Reef-Host")
		_ = s.agents.Control(host, generation, enabled)
		for _, capability := range input.Capabilities {
			if capability == agents.MonitoringCapability {
				_ = s.agents.Monitoring(host, generation, true)
			}
			if capability == agents.EventsCapability {
				_ = s.agents.Events(host, generation, true)
			}
			if capability == agents.UpdatesCapability {
				_ = s.agents.Updates(host, generation, true)
			}
			if capability == agents.BuildsCapability {
				_ = s.agents.Builds(host, generation, true)
			}
			if capability == agents.ImageBatchCapability {
				_ = s.agents.ImageBatch(host, generation, true)
			}
			if capability == agents.StatsCapability {
				_ = s.agents.Stats(host, generation, true)
			}
			if capability == agents.NetworksCapability {
				_ = s.agents.Networks(host, generation, true)
			}
			if capability == agents.VolumeBackupsCapability {
				_ = s.agents.VolumeBackups(host, generation, true)
			}
			if capability == agents.VolumesCapability {
				_ = s.agents.Volumes(host, generation, true)
			}
			if capability == agents.ImagesCapability {
				_ = s.agents.Images(host, generation, true)
			}
		}
		s.relay.Connect(host, generation)
		writeJSON(w, map[string]any{"generation": generation, "events": s.canAcceptEvents(), "monitoring": s.agents.CanMonitoring(host)})
	default:
		http.NotFound(w, r)
	}
}
func decodeAgent(r *http.Request, out any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return errors.New("Trailing JSON")
	}
	return nil
}
func bearer(r *http.Request) string {
	s := r.Header.Get("Authorization")
	if !strings.HasPrefix(s, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(s, "Bearer ")
}
func (s *Server) hosts(w http.ResponseWriter, r *http.Request) {
	if s.agents == nil {
		http.Error(w, "Agent registry unavailable", 503)
		return
	}
	if r.Method == "GET" {
		writeJSON(w, map[string]any{"enabled": s.cfg.AgentsEnabled, "hosts": s.filterHosts(s.requestUser(r), s.agents.Views()), "local_allowed": s.requestUser(r).Allows("local"), "version": buildinfo.Version, "updates_available": agentupdate.ValidTarget(buildinfo.Version, buildinfo.Revision)})
		return
	}
	switch r.Form.Get("action") {
	case "enroll":
		if !s.cfg.AgentsEnabled {
			http.Error(w, "Enable agents_enabled and configure HTTPS before enrolling agents", 409)
			return
		}
		token, e := s.agents.Token(r.Form.Get("name"))
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		writeJSON(w, map[string]string{"token": token})
		return
	case "rename", "revoke":
		if e := s.agents.Change(r.Form.Get("id"), r.Form.Get("name"), r.Form.Get("action") == "revoke"); e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		if view, _, ok := s.agents.Inventory(r.Form.Get("id")); ok {
			s.m.emit(notifications.New("agent", r.Form.Get("action"), "completed", "", view.Name))
		}
		if r.Form.Get("action") == "revoke" {
			s.relay.Disconnect(r.Form.Get("id"))
		}
		w.WriteHeader(204)
	default:
		http.Error(w, "Unknown host action", 400)
	}
}

func (s *Server) agentCommands(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var input struct {
		Generation string         `json:"generation"`
		Frames     []agents.Frame `json:"frames,omitempty"`
	}
	if decodeAgent(r, &input) != nil {
		http.Error(w, "Invalid agent command payload", 400)
		return
	}
	host := r.Header.Get("X-Reef-Host")
	if !s.agents.CanControl(host, input.Generation) {
		http.Error(w, "Inactive agent generation", 403)
		return
	}
	if r.URL.Path == "/agent/v1/results" {
		if err := s.relay.Results(host, input.Generation, input.Frames); err != nil {
			http.Error(w, "Agent response rejected", 409)
			return
		}
		w.WriteHeader(204)
		return
	}
	result, err := s.relay.Poll(r.Context(), host, input.Generation)
	if err != nil {
		http.Error(w, "Command poll rejected", 409)
		return
	}
	writeJSON(w, result)
}
