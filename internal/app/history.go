package app

import (
	"net/http"
	"strconv"

	"reef/internal/history"
	"reef/internal/notifications"
)

func (s *Server) recordEvent(e notifications.Event) {
	if s.history != nil {
		s.history.RecordLocal(e)
	}
	if s.webhooks != nil {
		s.webhooks.Emit(e)
	}
}
func (s *Server) canAcceptEvents() bool {
	return s.history != nil && s.history.Ready() || s.webhooks != nil && s.webhooks.Ready()
}

// History stays central even when the selected host is disconnected/revoked.
// This route is deliberately absent from the agent command allowlist.
func (s *Server) actionHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if s.history == nil {
		http.Error(w, "Action history unavailable", 503)
		return
	}
	offset := 0
	var err error
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "Invalid history page", 400)
			return
		}
	}
	u := s.requestUser(r)
	allowed := map[string]bool(nil)
	if !u.AllHosts {
		allowed = map[string]bool{}
		for _, id := range u.Hosts {
			allowed[id] = true
		}
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" {
		cursor, err = s.historyCursor(cursor, u, false)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	page, err := s.history.Query(history.Query{AllowedHosts: allowed, RestrictAdminEvents: !u.GlobalAdmin(), Host: r.URL.Query().Get("host"), Scope: r.URL.Query().Get("scope"), Status: r.URL.Query().Get("status"), Search: r.URL.Query().Get("search"), Cursor: cursor, Offset: offset})
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !u.Allows("local") {
		page.Dropped = 0
	}
	page.Cursor, err = s.historyCursor(page.Cursor, u, true)
	if err != nil {
		http.Error(w, "History page unavailable", 503)
		return
	}
	writeJSON(w, page)
}
