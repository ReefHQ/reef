package app

import (
	"net/http"
	"reef/internal/notifications"
)

func (m *Manager) emit(e notifications.Event) {
	if m.Notify != nil {
		m.Notify(e)
	}
}
func (m *Manager) projectEvent(p Project, action, status, operation string) {
	scope := "project"
	if action == "build" {
		scope = "build"
	}
	m.emit(notifications.New(scope, action, status, operation, p.ProjectName))
}
func (m *Manager) catalogEvent(scope, action, status, id, target string, total int, results []ImageResult) {
	e := notifications.New(scope, action, status, id, target)
	e.Count = total
	for _, r := range results {
		if r.Unprocessed {
			e.Skipped++
		} else if r.OK {
			e.Succeeded++
		} else {
			e.Failed++
		}
	}
	if status != "started" {
		e.Status = "completed"
		if e.Failed > 0 || e.Skipped > 0 {
			e.Status = "partial"
		}
		if e.Succeeded == 0 && (e.Failed > 0 || e.Skipped > 0) {
			e.Status = "failed"
		}
	}
	m.emit(e)
}
func (s *Server) webhookSettings(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		http.Error(w, "Webhooks are configured on central Reef only", 409)
		return
	}
	if r.Method == "GET" {
		writeJSON(w, s.webhooks.View())
		return
	}
	if r.Form.Get("confirmed") != "yes" {
		http.Error(w, "Confirm webhook settings or the test notification", 400)
		return
	}
	switch r.Form.Get("action") {
	case "save":
		if err := s.webhooks.Configure(notifications.Config{Enabled: r.Form.Get("enabled") == "true", Format: r.Form.Get("format"), URL: r.Form.Get("url")}, r.Form.Get("clear") == "yes"); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.recordEvent(notifications.New("webhook", "configure", "completed", "", "Webhook settings"))
	case "test":
		if !s.webhooks.View().Enabled {
			http.Error(w, "Save and enable the webhook before sending a test", 409)
			return
		}
		s.recordEvent(notifications.New("webhook", "test", "completed", "", "Test notification"))
	default:
		http.Error(w, "Unknown webhook action", 400)
		return
	}
	writeJSON(w, s.webhooks.View())
}

func (s *Server) agentEvents(w http.ResponseWriter, r *http.Request) {
	if !s.canAcceptEvents() {
		http.Error(w, "Central action event storage unavailable", 503)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	var input struct {
		Generation string                `json:"generation"`
		Events     []notifications.Event `json:"events"`
	}
	if decodeAgent(r, &input) != nil || len(input.Events) > 32 {
		http.Error(w, "Invalid action event payload", 400)
		return
	}
	host := r.Header.Get("X-Reef-Host")
	if !s.agents.CanControl(host, input.Generation) || !s.agents.CanEvents(host) {
		http.Error(w, "Inactive event connection", 403)
		return
	}
	view, _, ok := s.agents.Inventory(host)
	if !ok {
		http.Error(w, "Agent not found", 403)
		return
	}
	accepted := true
	if s.history != nil && s.history.Ready() {
		if err := s.history.Accept(host, view.Name, input.Events); err != nil {
			accepted = false
		}
	}
	if s.webhooks != nil && s.webhooks.Ready() {
		if err := s.webhooks.Accept(host, view.Name, input.Events); err != nil {
			accepted = false
		}
	}
	if !accepted {
		http.Error(w, "Action events could not be persisted; metadata will be retried", 503)
		return
	}
	ids := []string{}
	for _, e := range input.Events {
		ids = append(ids, e.ID)
	}
	writeJSON(w, map[string]any{"ack": ids})
}

func (a *AgentBackend) HasActionEvents() bool               { return a.events != nil && a.events.Ready() }
func (a *AgentBackend) ActionEvents() []notifications.Event { return a.events.Batch() }
func (a *AgentBackend) AckActionEvents(ids []string) error  { return a.events.Ack(ids) }
