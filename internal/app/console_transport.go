package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type consoleTransport struct {
	owner  string
	ctx    context.Context
	cancel context.CancelFunc
	events chan map[string]any
}

// One bounded output stream serves every console tab in a Reef page. Keeping
// long-lived HTTP/1 connections per shell would exhaust the browser's pool.
func (s *Server) consoleStream(w http.ResponseWriter, r *http.Request, sess session) {
	ctx, cancel := context.WithDeadline(r.Context(), sess.Expires)
	defer cancel()
	id := randomID()
	stream := &consoleTransport{owner: sess.CSRF, ctx: ctx, cancel: cancel, events: make(chan map[string]any, 64)}
	s.mu.Lock()
	if s.consoleStreams == nil {
		s.consoleStreams = map[string]*consoleTransport{}
	}
	owned := 0
	for _, existing := range s.consoleStreams {
		if existing.owner == sess.CSRF {
			owned++
		}
	}
	per, total := s.cfg.consoleLimits()
	if owned >= per || len(s.consoleStreams) >= total {
		s.mu.Unlock()
		http.Error(w, "Console connection limit reached. Close another Reef console panel first.", 429)
		return
	}
	s.consoleStreams[id] = stream
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.consoleStreams, id)
		for _, entry := range s.consoles {
			if entry.transport == id {
				entry.cancel()
			}
		}
		s.mu.Unlock()
	}()
	rc := http.NewResponseController(w)
	if e := rc.SetWriteDeadline(time.Time{}); e != nil {
		http.Error(w, "Console streaming is unavailable.", 503)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(w)
	send := func(frame map[string]any) bool {
		if rc.SetWriteDeadline(time.Now().Add(15*time.Second)) != nil {
			return false
		}
		return encoder.Encode(frame) == nil && rc.Flush() == nil
	}
	if !send(map[string]any{"type": "transport", "id": id}) {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-stream.events:
			if !send(frame) {
				return
			}
		case <-ticker.C:
			if _, ok := s.getSession(r); !ok {
				return
			}
			if !send(map[string]any{"type": "heartbeat"}) {
				return
			}
		}
	}
}
