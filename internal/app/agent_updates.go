package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reef/internal/agentupdate"
	"reef/internal/buildinfo"
	"reef/internal/notifications"
	"time"
)

// configureAgentUpdates is only called by the outbound agent, never central Reef.
func (a *AgentBackend) configureAgentUpdates(ctx context.Context, dir string) error {
	if dir == "" {
		return nil
	}
	store, err := agentupdate.Open(dir)
	if err != nil {
		return err
	}
	a.server.agentUpdates = store
	if err = a.server.syncAgentUpdate(); err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				_ = a.server.syncAgentUpdate()
			}
		}
	}()
	return nil
}
func (s *Server) syncAgentUpdate() error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.agentUpdates.Operation()
	if err != nil {
		return err
	}
	if o.Running() && !s.m.agentUpdating {
		s.m.agentUpdating = true
		s.m.storageBusy = true
		s.agentUpdating = true
	} else if !o.Running() && s.m.agentUpdating {
		status := "completed"
		if o.Phase == "failed" {
			status = "failed"
		} else if o.Phase != "completed" {
			status = "unknown"
		}
		s.m.emit(notifications.New("agent", "update", status, o.ID, o.Version))
		s.m.agentUpdating = false
		s.m.storageBusy = false
		s.agentUpdating = false
	}
	return nil
}
func (s *Server) agentUpdate(w http.ResponseWriter, r *http.Request) {
	if s.agentUpdates == nil {
		http.Error(w, "Agent updates need one-time setup on this host", 409)
		return
	}
	if r.Method == "GET" {
		o, err := s.agentUpdates.Operation()
		if err != nil {
			http.Error(w, "Cannot read agent update state", 503)
			return
		}
		writeJSON(w, map[string]any{"available": s.agentUpdates.Available(), "operation": o, "version": buildinfo.Version})
		return
	}
	if r.Form.Get("confirmed") != "yes" {
		http.Error(w, "Confirm the agent update", 400)
		return
	}
	if err := s.beginAgentUpdate(r.Form.Get("version"), r.Form.Get("revision")); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
func (s *Server) beginAgentUpdate(version, revision string) error {
	if !agentupdate.ValidTarget(version, revision) || !agentupdate.Newer(version, buildinfo.Version) {
		return errors.New("Choose a newer stable agent version matching central Reef")
	}
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.agentUpdates.Available() {
		return errors.New("Agent updater is unavailable; check its one-time setup on this host")
	}
	if s.m.storageBusy || s.m.agentUpdating {
		return errors.New("Wait for host operations before updating the agent")
	}
	if len(s.consoles) > 0 {
		return errors.New("Disconnect this host's consoles before updating its agent")
	}
	for _, p := range s.m.Projects {
		p.mu.Lock()
		busy := p.busy || p.ImageUpdates.Checking
		p.mu.Unlock()
		if busy {
			return errors.New("Wait for project operations and image checks before updating the agent")
		}
	}
	unlock, err := s.agentUpdates.Lock("request.lock")
	if err != nil {
		return err
	}
	defer unlock()
	previous, err := s.agentUpdates.Operation()
	if err != nil {
		return errors.New("Cannot read agent update state")
	}
	if previous.Running() {
		return errors.New("Agent update already running")
	}
	o := agentupdate.Operation{ID: randomID(), FromVersion: buildinfo.Version, Version: version, Revision: revision, Phase: "queued", Message: "Agent update accepted; waiting for the host updater.", Started: time.Now(), Updated: time.Now()}
	if err = s.agentUpdates.Write("operation.json", o); err != nil {
		return errors.New("Cannot persist agent update request")
	}
	s.m.emit(notifications.New("agent", "update", "started", o.ID, version))
	s.m.agentUpdating = true
	s.m.storageBusy = true
	s.agentUpdating = true
	return nil
}

// The browser cannot choose an image, registry, path, version or revision.
func (s *Server) centralAgentUpdate(w http.ResponseWriter, r *http.Request, sess session, host string) {
	if s.agents == nil || !s.agents.CanUpdates(host) {
		http.Error(w, "Upgrade and prepare this agent once to enable updates from Reef", 409)
		return
	}
	if r.Method == "POST" {
		if r.Form.Get("confirmed") != "yes" || r.Form.Get("version") != buildinfo.Version || !agentupdate.ValidTarget(buildinfo.Version, buildinfo.Revision) {
			http.Error(w, "Confirm the current central Reef version", 409)
			return
		}
		view, _, ok := s.agents.Inventory(host)
		if !ok || !agentupdate.Newer(buildinfo.Version, view.Version) {
			http.Error(w, "Agent is already current or newer than central Reef", 409)
			return
		}
		if s.monitor != nil && s.monitor.Enabled() {
			// Monitoring storage failure must not block the requested agent update.
			_ = s.monitor.Hold(host, view.Name, time.Now().Add(10*time.Minute))
		}
		r.PostForm = url.Values{"confirmed": {"yes"}, "version": {buildinfo.Version}, "revision": {buildinfo.Revision}}
	}
	s.remoteHTTP(w, r, sess, host)
}
