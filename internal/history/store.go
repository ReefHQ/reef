// Package history retains bounded action metadata independently of webhook delivery.
package history

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"reef/internal/notifications"
)

const MaxEvents = 2000
const MaxSeen = 4096
const MaxBytes = 8 << 20
const Retention = 30 * 24 * time.Hour
const QueueSize = 256

var hostID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var seenID = regexp.MustCompile(`^(local|[a-f0-9]{64}):[a-f0-9]{48}$`)
var unavailable = errors.New("Action history persistence unavailable; inspect its private state and restart Reef")

type entry struct {
	Sequence  uint64              `json:"sequence"`
	Recorded  time.Time           `json:"recorded"`
	Recovered bool                `json:"recovered,omitempty"`
	Event     notifications.Event `json:"event"`
}
type disk struct {
	Version  int      `json:"version"`
	Sequence uint64   `json:"sequence"`
	Entries  []entry  `json:"entries"`
	Seen     []string `json:"seen"`
	Dropped  uint64   `json:"dropped"`
}
type Store struct {
	mu               sync.Mutex
	file             string
	data             disk
	digest           [32]byte
	existed          bool
	persistenceError bool
	queue            chan notifications.Event
	dropped          atomic.Uint64
}

func Unavailable() *Store {
	return &Store{persistenceError: true, queue: make(chan notifications.Event, QueueSize), data: disk{Version: 1}}
}
func (s *Store) Ready() bool { return s != nil && s.file != "" }
func safeParents(dir string) error {
	for p := dir; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return unavailable
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return unavailable
	}
	return nil
}
func Open(state string) (*Store, error) {
	if !filepath.IsAbs(state) {
		return nil, unavailable
	}
	dir := filepath.Join(state, "history")
	// Refuse symlinks before creating anything, including a symlinked ancestor.
	for p := dir; ; p = filepath.Dir(p) {
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, unavailable
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if os.MkdirAll(dir, 0700) != nil || safeParents(dir) != nil {
		return nil, unavailable
	}
	s := &Store{file: filepath.Join(dir, "actions.json"), data: disk{Version: 1}, queue: make(chan notifications.Event, QueueSize)}
	info, err := os.Lstat(s.file)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > MaxBytes {
		return nil, unavailable
	}
	raw, err := os.ReadFile(s.file)
	if err != nil || len(raw) > MaxBytes {
		return nil, unavailable
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var extra any
	if dec.Decode(&s.data) != nil || dec.Decode(&extra) != io.EOF || s.data.Version != 1 || len(s.data.Entries) > MaxEvents || len(s.data.Seen) > MaxSeen {
		return nil, unavailable
	}
	var sequence uint64
	for _, item := range s.data.Entries {
		if item.Sequence <= sequence || item.Sequence > s.data.Sequence || item.Recorded.IsZero() || item.Recorded.After(time.Now().Add(5*time.Minute)) || !item.Event.Valid() || (item.Event.HostID != "local" && !hostID.MatchString(item.Event.HostID)) {
			return nil, unavailable
		}
		sequence = item.Sequence
	}
	for _, key := range s.data.Seen {
		if !seenID.MatchString(key) {
			return nil, unavailable
		}
	}
	s.existed = true
	s.digest = sha256.Sum256(raw)
	s.dropped.Store(s.data.Dropped)
	changed := s.pruneLocked(time.Now())
	// Central workers do not survive a process restart. An unmatched local start
	// is an unknown outcome, never an instruction to rerun the Docker operation.
	for i, item := range s.data.Entries {
		if item.Event.HostID == "local" && item.Event.Status == "started" && !item.Recovered {
			s.data.Entries[i].Recovered = true
			changed = true
		}
	}
	if changed {
		if err = s.saveLocked(); err != nil {
			s.persistenceError = true
		}
	}
	return s, nil
}
func (s *Store) saveLocked() error {
	fail := func() error { s.persistenceError = true; return unavailable }
	if !s.Ready() || safeParents(filepath.Dir(s.file)) != nil {
		return fail()
	}
	info, err := os.Lstat(s.file)
	if s.existed {
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > MaxBytes {
			return fail()
		}
		raw, e := os.ReadFile(s.file)
		if e != nil || len(raw) > MaxBytes || sha256.Sum256(raw) != s.digest {
			return fail()
		}
	} else if !os.IsNotExist(err) {
		return fail()
	}
	s.data.Dropped = s.dropped.Load()
	raw, err := json.Marshal(s.data)
	if err != nil || len(raw) > MaxBytes {
		return fail()
	}
	f, err := os.CreateTemp(filepath.Dir(s.file), ".actions-")
	if err != nil {
		return fail()
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err == nil {
		err = closed
	}
	if err != nil {
		return fail()
	}
	if os.Rename(f.Name(), s.file) != nil {
		return fail()
	}
	s.digest = sha256.Sum256(raw)
	s.existed = true
	dir, err := os.Open(filepath.Dir(s.file))
	if err != nil {
		return fail()
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		return fail()
	}
	s.persistenceError = false
	return nil
}
func (s *Store) pruneLocked(now time.Time) bool {
	keep := make([]entry, 0, len(s.data.Entries))
	for _, item := range s.data.Entries {
		if !item.Recorded.Before(now.Add(-Retention)) {
			keep = append(keep, item)
		}
	}
	if len(keep) > MaxEvents {
		keep = keep[len(keep)-MaxEvents:]
	}
	changed := len(keep) != len(s.data.Entries)
	s.data.Entries = keep
	return changed
}
func (s *Store) append(events []notifications.Event) error {
	if !s.Ready() || len(events) > 32 {
		return unavailable
	}
	for _, e := range events {
		if !e.Valid() || (e.HostID != "local" && !hostID.MatchString(e.HostID)) {
			return errors.New("Invalid action event")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data
	next := old
	next.Entries = append([]entry{}, old.Entries...)
	next.Seen = append([]string{}, old.Seen...)
	seen := map[string]bool{}
	for _, key := range next.Seen {
		seen[key] = true
	}
	now := time.Now().UTC()
	changed := false
	for _, e := range events {
		if seen[e.Key()] {
			continue
		}
		seen[e.Key()] = true
		next.Seen = append(next.Seen, e.Key())
		changed = true
		if len(next.Seen) > MaxSeen {
			next.Seen = next.Seen[len(next.Seen)-MaxSeen:]
		}
		if e.Time.Before(now.Add(-Retention)) {
			continue
		}
		if next.Sequence == ^uint64(0) {
			s.persistenceError = true
			return unavailable
		}
		next.Sequence++
		e.Target = notifications.Text(e.Target, 160)
		e.Host = notifications.Text(e.Host, 200)
		next.Entries = append(next.Entries, entry{Sequence: next.Sequence, Recorded: now, Event: e})
	}
	s.data = next
	if s.pruneLocked(now) {
		changed = true
	}
	if !changed {
		return nil
	}
	if err := s.saveLocked(); err != nil {
		s.data = old
		return err
	}
	return nil
}

// RecordLocal never waits for disk or history locks on an action's goroutine.
func (s *Store) RecordLocal(e notifications.Event) {
	if s == nil || !e.Valid() {
		return
	}
	e.HostID = "local"
	e.Host = "Local"
	if !s.Ready() {
		s.dropped.Add(1)
		return
	}
	select {
	case s.queue <- e:
	default:
		s.dropped.Add(1)
	}
}
func (s *Store) Accept(host, name string, events []notifications.Event) error {
	if !hostID.MatchString(host) || len(events) > 32 {
		return errors.New("Invalid action event batch")
	}
	bound := make([]notifications.Event, 0, len(events))
	for _, e := range events {
		if !e.Valid() || (e.Scope == "webhook" || e.Scope == "monitor") {
			return errors.New("Invalid action event")
		}
		e.HostID = host
		e.Host = notifications.Text(name, 200)
		bound = append(bound, e)
	}
	return s.append(bound)
}
func (s *Store) flush(first notifications.Event) {
	batch := []notifications.Event{first}
	for len(batch) < 32 {
		select {
		case e := <-s.queue:
			batch = append(batch, e)
		default:
			if s.append(batch) != nil {
				s.dropped.Add(uint64(len(batch)))
			}
			return
		}
	}
	if s.append(batch) != nil {
		s.dropped.Add(uint64(len(batch)))
	}
}

// Run performs bounded local persistence and hourly retention maintenance. Local
// metadata lost on a crash/full queue/disk failure is reported, never replayed as work.
func (s *Store) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case e := <-s.queue:
			s.flush(e)
		case <-ticker.C:
			s.mu.Lock()
			if s.pruneLocked(time.Now()) {
				_ = s.saveLocked()
			}
			s.mu.Unlock()
		case <-ctx.Done():
			for {
				select {
				case e := <-s.queue:
					s.flush(e)
				default:
					return
				}
			}
		}
	}
}

// AcceptCentral records trusted central metadata without rebinding it to Local.
func (s *Store) AcceptCentral(e notifications.Event) error { return s.append([]notifications.Event{e}) }
