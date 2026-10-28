package monitoring

import (
	"reef/internal/notifications"
	"sort"
	"sync"
	"time"
)

type tick struct {
	At    time.Time
	Count int64
}
type watch struct {
	Bad, Good, Last time.Time
	Restarts        int64
	Baseline        bool
	Ticks           []tick
}
type runtimeHost struct {
	Coverage
	Watches  map[string]*watch
	Since    time.Time
	Sequence uint64
}
type disk struct {
	Version   int                   `json:"version"`
	Config    Config                `json:"config"`
	Incidents []Incident            `json:"incidents"`
	Pending   []notifications.Event `json:"pending"`
	Holds     map[string]time.Time  `json:"holds"`
}
type Store struct {
	mu               sync.Mutex
	file             string
	data             disk
	hosts            map[string]*runtimeHost
	started          time.Time
	digest           [32]byte
	existed          bool
	limited          bool
	persistenceError bool
	now              func() time.Time
}
type View struct {
	Config           Config     `json:"config"`
	Incidents        []Incident `json:"incidents"`
	Hosts            []Coverage `json:"hosts"`
	Pending          int        `json:"pending"`
	PersistenceError bool       `json:"persistence_error"`
	CapacityLimited  bool       `json:"capacity_limited"`
	Checked          time.Time  `json:"checked_at"`
}

func Unavailable() *Store {
	return &Store{data: disk{Version: 1, Config: Defaults(), Holds: map[string]time.Time{}}, hosts: map[string]*runtimeHost{}, started: time.Now(), now: time.Now, persistenceError: true}
}
func (s *Store) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file != "" && s.data.Config.Enabled
}
func (s *Store) View() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.clone()
	s.prune(s.now())
	if (len(old.Incidents) != len(s.data.Incidents) || len(old.Holds) != len(s.data.Holds)) && s.file != "" {
		if s.saveLocked() != nil {
			s.data = old
		}
	}
	v := View{CapacityLimited: s.limited, Config: s.data.Config, Incidents: append([]Incident{}, s.data.Incidents...), Hosts: []Coverage{}, Pending: len(s.data.Pending), PersistenceError: s.persistenceError || s.file == "", Checked: s.now().UTC()}
	for _, h := range s.hosts {
		c := h.Coverage
		c.HoldUntil = s.data.Holds[h.HostID]
		if !c.HoldUntil.IsZero() && c.HoldUntil.After(v.Checked) {
			c.Status = "maintenance"
		}
		if v.Config.Enabled && c.Status == "ready" && v.Checked.Sub(c.Checked) > 90*time.Second {
			c.Status = "collection_unavailable"
		}
		v.Hosts = append(v.Hosts, c)
	}
	sort.Slice(v.Hosts, func(i, j int) bool { return v.Hosts[i].Host < v.Hosts[j].Host })
	sort.Slice(v.Incidents, func(i, j int) bool { return v.Incidents[i].Updated.After(v.Incidents[j].Updated) })
	return v
}
func (s *Store) clone() disk {
	d := s.data
	d.Incidents = append([]Incident{}, d.Incidents...)
	d.Pending = append([]notifications.Event{}, d.Pending...)
	d.Holds = map[string]time.Time{}
	for k, v := range s.data.Holds {
		d.Holds[k] = v
	}
	return d
}
func (s *Store) Configure(c Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !c.Valid() {
		return errorsConfig()
	}
	if s.file == "" {
		return ErrState
	}
	if c == s.data.Config {
		return nil
	}
	old := s.clone()
	s.data.Config = c
	s.data.Pending = nil
	// Policy changes start new baselines; they must not masquerade as recovery.
	for i := range s.data.Incidents {
		if s.data.Incidents[i].Status == "firing" {
			s.retire(i, "Monitoring policy changed", s.now())
		}
	}
	if e := s.saveLocked(); e != nil {
		s.data = old
		return e
	}
	s.hosts = map[string]*runtimeHost{}
	s.started = s.now()
	return nil
}
func (s *Store) Hold(host, name string, until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ValidHost(host) || until.After(s.now().Add(15*time.Minute)) {
		return ErrState
	}
	s.data.Holds[host] = until
	if len(s.data.Holds) > 65 {
		return ErrState
	}
	if e := s.saveLocked(); e != nil {
		// Keep the conservative hold in memory even if its persistence fails.
		return e
	}
	return nil
}
func (s *Store) host(id, name string) *runtimeHost {
	h := s.hosts[id]
	if h == nil {
		h = &runtimeHost{Coverage: Coverage{HostID: id}, Watches: map[string]*watch{}}
		s.hosts[id] = h
	}
	h.Host = notifications.Text(name, 200)
	return h
}
func (s *Store) active(host, kind, target string) int {
	for i, v := range s.data.Incidents {
		if v.HostID == host && v.Kind == kind && v.TargetID == target && v.Status == "firing" {
			return i
		}
	}
	return -1
}
func (s *Store) retire(i int, note string, now time.Time) {
	v := &s.data.Incidents[i]
	v.Status = "retired"
	v.Note = note
	v.Updated = now
	v.Closed = now
}
func (s *Store) transition(h *runtimeHost, kind, id, name string, bad, good bool, wait time.Duration, now time.Time) {
	key := kind + ":" + id
	w := h.Watches[key]
	if w == nil {
		w = &watch{}
		h.Watches[key] = w
	}
	// Missing collection time never counts toward a sustained observation.
	if now.Sub(w.Last) > 90*time.Second {
		w.Bad = time.Time{}
		w.Good = time.Time{}
	}
	w.Last = now
	index := s.active(h.HostID, kind, id)
	if bad {
		w.Good = time.Time{}
		if w.Bad.IsZero() {
			w.Bad = now
		}
		if index < 0 && now.Sub(w.Bad) >= wait {
			if len(s.data.Incidents) >= MaxIncidents {
				for i, v := range s.data.Incidents {
					if v.Status != "firing" {
						s.data.Incidents = append(s.data.Incidents[:i], s.data.Incidents[i+1:]...)
						break
					}
				}
			}
			if len(s.data.Pending) >= MaxPending || len(s.data.Incidents) >= MaxIncidents {
				s.limited = true
				return
			}
			event := notifications.New("monitor", kind, "failed", "", name)
			event.Operation = event.ID
			event.Time = now
			event.HostID = h.HostID
			event.Host = h.Host
			s.data.Incidents = append(s.data.Incidents, Incident{ID: event.ID, HostID: h.HostID, Host: h.Host, Kind: kind, TargetID: id, Target: notifications.Text(name, 160), Status: "firing", Opened: now, Updated: now})
			s.data.Pending = append(s.data.Pending, event)
		}
		return
	}
	w.Bad = time.Time{}
	if !good {
		w.Good = time.Time{}
		return
	}
	if index < 0 {
		w.Good = time.Time{}
		return
	}
	if w.Good.IsZero() {
		w.Good = now
	}
	if now.Sub(w.Good) < time.Duration(s.data.Config.RecoverySeconds)*time.Second {
		return
	}
	if len(s.data.Pending) >= MaxPending {
		s.limited = true
		return
	}
	v := &s.data.Incidents[index]
	event := notifications.New("monitor", kind, "completed", v.ID, name)
	event.Time = now
	event.HostID = h.HostID
	event.Host = h.Host
	s.data.Pending = append(s.data.Pending, event)
	v.Status = "resolved"
	v.Updated = now
	v.Closed = now
	v.Note = "Observed recovery"
	w.Good = time.Time{}
}

// HostStatus uses central connection state, never an agent's wall clock.
func (s *Store) HostStatus(id, name, status string, last time.Time, supported bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ValidHost(id) || len(s.hosts) >= 65 && s.hosts[id] == nil {
		return
	}
	now := s.now().UTC()
	h := s.host(id, name)
	if !s.data.Config.Enabled {
		h.Status = "disabled"
		return
	}
	if status == "revoked" {
		old := s.clone()
		for i, v := range s.data.Incidents {
			if v.HostID == id && v.Status == "firing" {
				s.retire(i, "Host revoked", now)
			}
		}
		delete(s.data.Holds, id)
		delete(s.hosts, id)
		if !sameDisk(old, s.data) && s.saveLocked() != nil {
			s.data = old
		}
		return
	}
	if s.data.Holds[id].After(now) {
		h.Status = "maintenance"
		h.Watches = map[string]*watch{}
		return
	}
	old := s.clone()
	if status != "connected" {
		h.Status = "disconnected"
		h.Watches = map[string]*watch{} // Preserve the disconnect timer separately below.
		// Delay after enable/restart as well as after last contact. No blind startup alarm.
		since := last
		if since.IsZero() || since.Before(s.started) {
			since = s.started
		}
		if now.Sub(since) >= time.Duration(s.data.Config.DisconnectSeconds)*time.Second {
			s.transition(h, "disconnected", id, name, true, true, 0, now)
		}
	} else {
		s.transition(h, "disconnected", id, name, false, true, 0, now)
		if !supported {
			h.Status = "upgrade_required"
		} else if h.Checked.IsZero() || h.Status == "disconnected" || h.Status == "upgrade_required" || h.Status == "disabled" {
			h.Status = "waiting"
		}
	}
	s.prune(now)
	if !sameDisk(old, s.data) && s.saveLocked() != nil {
		s.data = old
	}
}

// Observe accepts only fresh sample sequences. Errors suspend state, never recover it.
func (s *Store) Observe(id, name string, sample Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ValidHost(id) || !sample.Valid() || !s.data.Config.Enabled || len(s.hosts) >= 65 && s.hosts[id] == nil {
		return
	}
	h := s.host(id, name)
	now := s.now().UTC()
	if sample.Maintenance || s.data.Holds[id].After(now) {
		h.Status = "maintenance"
		h.Watches = map[string]*watch{}
		return
	}
	if sample.Sequence <= h.Sequence || !h.Checked.IsZero() && now.Sub(h.Checked) < 20*time.Second {
		return
	}
	h.Sequence = sample.Sequence
	h.Checked = now
	h.Containers = len(sample.Containers)
	h.Failed = 0
	if sample.Error != "" {
		h.Status = "collection_unavailable"
		h.Watches = map[string]*watch{}
		return
	}
	old := s.clone()
	s.limited = false
	h.Status = "ready"
	seen := map[string]bool{}
	for _, c := range sample.Containers {
		seen[c.ID] = true
		if c.Error {
			h.Failed++
			delete(h.Watches, "unhealthy:"+c.ID)
			delete(h.Watches, "restart_loop:"+c.ID)
			continue
		}
		active := c.State == "running" || c.State == "restarting"
		if !active {
			for i, v := range s.data.Incidents {
				if v.HostID == id && v.TargetID == c.ID && v.Status == "firing" {
					s.retire(i, "Container is no longer running; recovery not asserted", now)
				}
			}
			delete(h.Watches, "unhealthy:"+c.ID)
			delete(h.Watches, "restart_loop:"+c.ID)
			continue
		}
		s.transition(h, "unhealthy", c.ID, c.Name, c.Health == "unhealthy", c.Health == "healthy", time.Duration(s.data.Config.UnhealthySeconds)*time.Second, now)
		if c.Health == "" {
			if i := s.active(id, "unhealthy", c.ID); i >= 0 {
				s.retire(i, "Healthcheck no longer available", now)
			}
		}
		key := "restart_loop:" + c.ID
		w := h.Watches[key]
		if w == nil {
			w = &watch{}
			h.Watches[key] = w
		}
		if !w.Baseline || c.Restarts < w.Restarts || now.Sub(w.Last) > 90*time.Second {
			w.Ticks = nil
			w.Baseline = true
			w.Bad = time.Time{}
			w.Good = time.Time{}
		} else if c.Restarts > w.Restarts {
			w.Ticks = append(w.Ticks, tick{now, c.Restarts - w.Restarts})
		}
		w.Restarts = c.Restarts
		window := time.Duration(s.data.Config.RestartWindowSeconds) * time.Second
		kept := []tick{}
		var count int64
		for _, t := range w.Ticks {
			if now.Sub(t.At) < window {
				kept = append(kept, t)
				count += t.Count
			}
		}
		w.Ticks = kept
		s.transition(h, "restart_loop", c.ID, c.Name, count >= int64(s.data.Config.RestartCount), c.State == "running" && count == 0, 0, now)
	}
	for i, v := range s.data.Incidents {
		if v.HostID == id && v.Kind != "disconnected" && v.Status == "firing" && !seen[v.TargetID] {
			s.retire(i, "Container no longer present; recovery not asserted", now)
		}
	}
	for key := range h.Watches {
		if key != "disconnected:"+id && len(key) > 64 && !seen[key[len(key)-64:]] {
			delete(h.Watches, key)
		}
	}
	if h.Failed > 0 {
		h.Status = "partial"
	}
	s.prune(now)
	if !sameDisk(old, s.data) && s.saveLocked() != nil {
		s.data = old
	}
}
func (s *Store) prune(now time.Time) {
	kept := []Incident{}
	for _, v := range s.data.Incidents {
		if v.Status == "firing" || now.Sub(v.Closed) < 30*24*time.Hour {
			kept = append(kept, v)
		}
	}
	for len(kept) > MaxIncidents {
		index := -1
		for i, v := range kept {
			if v.Status != "firing" {
				index = i
				break
			}
		}
		if index < 0 {
			break
		}
		kept = append(kept[:index], kept[index+1:]...)
	}
	s.data.Incidents = kept
	for id, until := range s.data.Holds {
		if now.After(until) {
			delete(s.data.Holds, id)
		}
	}
}
func (s *Store) Pending() []notifications.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	end := min(32, len(s.data.Pending))
	return append([]notifications.Event{}, s.data.Pending[:end]...)
}
func (s *Store) Ack(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.clone()
	for i, e := range s.data.Pending {
		if e.ID == id {
			s.data.Pending = append(s.data.Pending[:i], s.data.Pending[i+1:]...)
			break
		}
	}
	if e := s.saveLocked(); e != nil {
		s.data = old
		return e
	}
	return nil
}

// PolicyEpoch excludes a cached sample captured before enablement or a policy
// change. Receiving the same old heartbeat must not establish a fresh baseline.
func (s *Store) PolicyEpoch() time.Time { s.mu.Lock(); defer s.mu.Unlock(); return s.started }
