package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Config struct {
	Enabled bool   `json:"enabled"`
	Format  string `json:"format"`
	URL     string `json:"url"`
}
type pending struct {
	Event    Event     `json:"event"`
	Attempts int       `json:"attempts"`
	Next     time.Time `json:"next"`
}
type disk struct {
	Activated   time.Time `json:"activated"`
	Version     int       `json:"version"`
	Config      Config    `json:"config"`
	Pending     []pending `json:"pending"`
	Seen        []string  `json:"seen"`
	Sent        uint64    `json:"sent"`
	Dropped     uint64    `json:"dropped"`
	LastSuccess time.Time `json:"last_success"`
	LastError   string    `json:"last_error"`
	After       time.Time `json:"after"`
}
type View struct {
	Enabled          bool      `json:"enabled"`
	Format           string    `json:"format"`
	Configured       bool      `json:"configured"`
	Destination      string    `json:"destination"`
	Pending          int       `json:"pending"`
	Sent             uint64    `json:"sent"`
	Dropped          uint64    `json:"dropped"`
	LastSuccess      time.Time `json:"last_success"`
	LastError        string    `json:"last_error"`
	PersistenceError bool      `json:"persistence_error"`
}
type Service struct {
	mu               sync.Mutex
	file             string
	relay            bool
	data             disk
	persistenceError bool
	revision         uint64
	inflight         context.CancelFunc
}

// Unavailable keeps optional notification state failures separate from Docker control.
func Unavailable(relay bool) *Service {
	return &Service{relay: relay, persistenceError: true, data: disk{Version: 1, Config: Config{Format: "discord"}, LastError: "Notification state unavailable; inspect the private notification state before reconfiguring"}}
}
func (s *Service) Ready() bool { return s.file != "" }
func Open(state string, relay bool) (*Service, error) {
	dir := filepath.Join(state, "notifications")
	for p := dir; ; p = filepath.Dir(p) {
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("Notification state must not contain symlinks")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("Notification state must be private")
	}
	s := &Service{file: filepath.Join(dir, "state.json"), relay: relay, data: disk{Version: 1, Config: Config{Format: "discord"}}}
	info, err = os.Lstat(s.file)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 2<<20 {
		return nil, errors.New("Unsafe notification state")
	}
	b, err := os.ReadFile(s.file)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(b, &s.data) != nil || s.data.Version != 1 || len(s.data.Pending) > MaxPending || len(s.data.Seen) > 4096 {
		return nil, errors.New("Invalid notification state")
	}
	if s.data.Config.Enabled || s.data.Config.URL != "" {
		if _, err = validate(s.data.Config); err != nil {
			return nil, err
		}
	}
	for _, item := range s.data.Pending {
		if !item.Event.Valid() || item.Attempts < 0 || item.Attempts > 5 {
			return nil, errors.New("Invalid queued notification")
		}
	}
	return s, nil
}
func (s *Service) saveLocked() error {
	b, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	if info, e := os.Lstat(s.file); e == nil && !info.Mode().IsRegular() {
		s.persistenceError = true
		return errors.New("Unsafe notification state")
	}
	f, err := os.CreateTemp(filepath.Dir(s.file), ".notifications-")
	if err != nil {
		s.persistenceError = true
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err == nil {
		err = os.Rename(f.Name(), s.file)
	}
	if err == nil {
		var d *os.File
		d, err = os.Open(filepath.Dir(s.file))
		if err == nil {
			err = d.Sync()
			d.Close()
		}
	}
	s.persistenceError = err != nil
	return err
}
func (s *Service) View() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, _ := validate(s.data.Config)
	destination := ""
	if u != nil {
		destination = u.Host
	}
	return View{Enabled: s.data.Config.Enabled, Format: s.data.Config.Format, Configured: s.data.Config.URL != "", Destination: destination, Pending: len(s.data.Pending), Sent: s.data.Sent, Dropped: s.data.Dropped, LastSuccess: s.data.LastSuccess, LastError: s.data.LastError, PersistenceError: s.persistenceError}
}
func (s *Service) Configure(c Config, clear bool) error {
	if !s.Ready() {
		return errors.New("Notification state unavailable; repair its private state and restart Reef")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.relay {
		return errors.New("Webhooks are configured on central Reef only")
	}
	if c.URL == "" && !clear {
		c.URL = s.data.Config.URL
	}
	if c.Enabled || c.URL != "" {
		if _, err := validate(c); err != nil {
			return err
		}
	}
	if c.Format != "discord" && c.Format != "json" {
		return errors.New("Choose Discord or JSON format")
	}
	if c == s.data.Config {
		return nil
	}
	old := s.data
	s.data.Activated = time.Now()
	s.data.Config = c
	s.data.Pending = nil
	s.data.After = time.Time{}
	s.data.LastError = ""
	if err := s.saveLocked(); err != nil {
		s.data = old
		return errors.New("Cannot persist webhook settings")
	}
	s.revision++
	if s.inflight != nil {
		s.inflight()
	}
	return nil
}

// Emit is bounded disk work only. Delivery never runs on an action's goroutine.
func (s *Service) Emit(e Event) {
	if !s.Ready() {
		return
	}
	if !e.Valid() {
		return
	}
	e.Target = Text(e.Target, 160)
	e.Host = Text(e.Host, 200)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.relay && !s.data.Config.Enabled {
		return
	}
	if len(s.data.Pending) >= MaxPending {
		s.data.Dropped++
		_ = s.saveLocked()
		return
	}
	s.data.Pending = append(s.data.Pending, pending{Event: e})
	_ = s.saveLocked()
}
func (s *Service) Batch() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := []Event{}
	for _, item := range s.data.Pending {
		if len(events) == 32 {
			break
		}
		events = append(events, item.Event)
	}
	return events
}
func (s *Service) Ack(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data.Pending
	remove := map[string]bool{}
	for _, id := range ids {
		remove[id] = true
	}
	remaining := []pending{}
	for _, item := range old {
		if !remove[item.Event.ID] {
			remaining = append(remaining, item)
		}
	}
	s.data.Pending = remaining
	if err := s.saveLocked(); err != nil {
		s.data.Pending = old
		return err
	}
	return nil
}

// Accept binds remote events to the authenticated registry host and deduplicates
// replayed batches. ACK only follows durable central acceptance.
func (s *Service) Accept(host, name string, events []Event) error {
	return s.accept(host, name, events, false)
}
func (s *Service) AcceptCentral(e Event) error { return s.accept(e.HostID, e.Host, []Event{e}, true) }
func (s *Service) accept(host, name string, events []Event, central bool) error {
	if !s.Ready() {
		return errors.New("Notification state unavailable")
	}
	if len(events) > 32 {
		return errors.New("Event batch exceeds limit")
	}
	for _, e := range events {
		if !e.Valid() || e.Scope == "webhook" || ((e.Scope == "monitor" || e.Scope == "schedule") && !central) {
			return errors.New("Invalid action event")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data
	next := s.data
	next.Pending = append([]pending{}, old.Pending...)
	next.Seen = append([]string{}, old.Seen...)
	seen := map[string]bool{}
	for _, key := range next.Seen {
		seen[key] = true
	}
	for _, e := range events {
		e.HostID, e.Host = host, Text(name, 200)
		e.Target = Text(e.Target, 160)
		if seen[e.Key()] {
			continue
		}
		if next.Config.Enabled && !e.Time.Before(next.Activated) && time.Since(e.Time) < 24*time.Hour {
			if len(next.Pending) >= MaxPending {
				return errors.New("Notification queue is full")
			}
			next.Pending = append(next.Pending, pending{Event: e})
		}
		seen[e.Key()] = true
		next.Seen = append(next.Seen, e.Key())
		if len(next.Seen) > 4096 {
			next.Seen = next.Seen[1:]
		}
	}
	s.data = next
	if err := s.saveLocked(); err != nil {
		s.data = old
		return errors.New("Cannot persist action events")
	}
	return nil
}
