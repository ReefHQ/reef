package app

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"reef/internal/docker"
	"reef/internal/monitoring"
)

// Host maintenance includes all admitted Reef mutations. External Docker clients
// and commands in consoles cannot be inferred and are not automatically muted.
func (s *Server) monitoringBusy() bool {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	if s.m.storageBusy || s.m.agentUpdating {
		return true
	}
	for _, p := range s.m.Projects {
		p.mu.Lock()
		busy := p.busy
		p.mu.Unlock()
		if busy {
			return true
		}
	}
	return false
}
func (a *AgentBackend) MonitoringBusy() bool { return a.server.monitoringBusy() }
func (s *Server) collectMonitoring(ctx context.Context) monitoring.Sample {
	sample := monitoring.Sample{Sequence: 1, Containers: []monitoring.Container{}}
	if s.monitoringBusy() {
		sample.Maintenance = true
		return sample
	}
	reader, ok := s.discovery.(docker.MonitorReader)
	if !ok {
		sample.Error = "Collection unavailable"
		return sample
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	sample = docker.CollectMonitor(ctx, s.discovery, reader)
	sample.Maintenance = s.monitoringBusy()
	return sample
}
func (a *AgentBackend) MonitoringSnapshot(ctx context.Context) monitoring.Sample {
	return a.server.collectMonitoring(ctx)
}
func (s *Server) monitoringSettings(w http.ResponseWriter, r *http.Request) {
	if s.monitor == nil {
		http.Error(w, "Monitoring is configured on central Reef only", 409)
		return
	}
	if r.Method == "POST" {
		if r.Form.Get("confirmed") != "yes" {
			http.Error(w, "Confirm monitoring policy changes", 400)
			return
		}
		if r.Form.Get("enabled") != "true" && r.Form.Get("enabled") != "false" {
			http.Error(w, "Invalid monitoring policy", 400)
			return
		}
		c := monitoring.Config{Enabled: r.Form.Get("enabled") == "true"}
		fields := []struct {
			name  string
			value *int
		}{{"disconnect_seconds", &c.DisconnectSeconds}, {"unhealthy_seconds", &c.UnhealthySeconds}, {"restart_count", &c.RestartCount}, {"restart_window_seconds", &c.RestartWindowSeconds}, {"recovery_seconds", &c.RecoverySeconds}}
		for _, f := range fields {
			n, e := strconv.Atoi(r.Form.Get(f.name))
			if e != nil {
				http.Error(w, "Invalid monitoring thresholds", 400)
				return
			}
			*f.value = n
		}
		if e := s.monitor.Configure(c); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
	} else if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	writeJSON(w, s.monitor.View())
}
func (s *Server) dispatchMonitoring() {
	for _, event := range s.monitor.Pending() {
		accepted := true
		if s.history == nil || !s.history.Ready() {
			accepted = false
		} else if e := s.history.AcceptCentral(event); e != nil {
			accepted = false
		}
		if s.webhooks == nil || !s.webhooks.Ready() {
			accepted = false
		} else if e := s.webhooks.AcceptCentral(event); e != nil {
			accepted = false
		}
		if !accepted {
			return
		}
		if s.monitor.Ack(event.ID) != nil {
			return
		}
	}
}
func (s *Server) observeMonitoringHosts() {
	if s.monitor == nil || !s.monitor.Enabled() {
		return
	}
	if s.agents != nil {
		for _, h := range s.agents.Views() {
			supported := s.agents.CanMonitoring(h.ID)
			s.monitor.HostStatus(h.ID, h.Name, h.Status, h.LastContact, supported)
			if h.Status != "connected" || !supported {
				continue
			}
			sample, at, ok := s.agents.MonitorSample(h.ID)
			if ok && !at.Before(s.monitor.PolicyEpoch()) && time.Since(at) < 90*time.Second {
				s.monitor.Observe(h.ID, h.Name, sample)
			}
		}
	}
}
func (s *Server) runMonitoring(ctx context.Context) {
	// Independent local collector: remote heartbeat/alert evaluation and delivery
	// never wait for an unavailable local Docker socket.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var sequence uint64
		for {
			if s.monitor.Enabled() {
				epoch := s.monitor.PolicyEpoch()
				sample := s.collectMonitoring(ctx)
				sequence++
				sample.Sequence = sequence
				if s.monitor.Enabled() && s.monitor.PolicyEpoch().Equal(epoch) {
					s.monitor.Observe("local", "Local", sample)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(monitoring.SampleInterval):
			}
		}
	}()
	defer wg.Wait()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.observeMonitoringHosts()
			s.dispatchMonitoring()
		}
	}
}
