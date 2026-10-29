// Package schedules persists explicitly approved central schedules and claims a
// slot before dispatch. Interrupted claims are never retried automatically.
package schedules

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

const MaxJobs = 128

var id = regexp.MustCompile(`^[a-f0-9]{48}$`)
var key = regexp.MustCompile(`^[a-f0-9]{64}$`)
var clock = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)
var target = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)
var ErrState = errors.New("Schedule storage unavailable or changed; inspect private state and restart Reef")
var ErrChanged = errors.New("Schedule changed; refresh before editing")

type Result struct {
	ID        string    `json:"id"`
	Due       time.Time `json:"due"`
	Started   time.Time `json:"started"`
	Finished  time.Time `json:"finished"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	Operation string    `json:"operation,omitempty"`
}
type Job struct {
	ID             string    `json:"id"`
	Revision       uint64    `json:"revision"`
	Name           string    `json:"name"`
	Host           string    `json:"host"`
	Action         string    `json:"action"`
	Target         string    `json:"target"`
	Fingerprint    string    `json:"fingerprint"`
	Frequency      string    `json:"frequency"`
	Weekday        int       `json:"weekday"`
	Time           string    `json:"time"`
	Zone           string    `json:"zone"`
	AgeDays        int       `json:"age_days"`
	MaxItems       int       `json:"max_items"`
	StopConsumers  bool      `json:"stop_consumers"`
	Enabled        bool      `json:"enabled"`
	Owner          string    `json:"owner"`
	OwnerRevision  uint64    `json:"owner_revision"`
	Next           time.Time `json:"next"`
	LastSlot       string    `json:"last_slot"`
	ReviewRequired bool      `json:"review_required"`
	Last           Result    `json:"last"`
}
type disk struct {
	Version int   `json:"version"`
	Jobs    []Job `json:"jobs"`
}
type Store struct {
	mu      sync.Mutex
	file    string
	data    disk
	hash    [32]byte
	existed bool
	broken  bool
}

func valid(j Job) bool {
	if !id.MatchString(j.ID) || j.Revision == 0 || len(j.Name) < 1 || len(j.Name) > 80 || strings.TrimSpace(j.Name) != j.Name || strings.ContainsAny(j.Name, "\r\n\x00") || (!key.MatchString(j.Host) && j.Host != "local") || !key.MatchString(j.Owner) || j.OwnerRevision == 0 || !clock.MatchString(j.Time) || len(j.Zone) > 100 || j.Weekday < 0 || j.Weekday > 6 || (j.Frequency != "daily" && j.Frequency != "weekly") || len(j.LastSlot) > 32 {
		return false
	}
	if _, e := time.LoadLocation(j.Zone); e != nil || j.Zone == "" || j.Zone == "Local" {
		return false
	}
	switch j.Action {
	case "image_check":
		if !target.MatchString(j.Target) || !key.MatchString(j.Fingerprint) || j.StopConsumers || j.AgeDays != 0 || j.MaxItems != 0 {
			return false
		}
	case "volume_backup":
		if !target.MatchString(j.Target) || !key.MatchString(j.Fingerprint) || !j.StopConsumers || j.AgeDays != 0 || j.MaxItems != 0 {
			return false
		}
	case "image_cleanup", "cache_cleanup":
		if j.Target != "" || j.Fingerprint != "" || j.StopConsumers || j.AgeDays < 1 || j.AgeDays > 365 || j.MaxItems < 1 || j.MaxItems > 2000 {
			return false
		}
	default:
		return false
	}
	r := j.Last
	if j.LastSlot != "" {
		if _, e := time.Parse("2006-01-02T15:04", j.LastSlot); e != nil {
			return false
		}
	}
	if r.ID == "" && r != (Result{}) {
		return false
	}
	if r.ID != "" {
		if !id.MatchString(r.ID) || r.Due.IsZero() || len(r.Message) > 300 || len(r.Operation) > 48 {
			return false
		}
		if r.Operation != "" && !id.MatchString(r.Operation) {
			return false
		}
		if r.Status == "running" && r.Started.IsZero() {
			return false
		}
		if r.Status != "running" && r.Finished.IsZero() {
			return false
		}
		switch r.Status {
		case "running", "completed", "failed", "skipped", "unknown":
		default:
			return false
		}
	}
	return !j.Next.IsZero() && (!j.ReviewRequired || !j.Enabled)
}

// Iterate UTC minutes so spring gaps have no occurrence and fall folds can be
// deduplicated by local date/time. No ambiguous time.Date normalization is used.
func Next(j Job, after time.Time, lastSlot string) (time.Time, error) {
	loc, e := time.LoadLocation(j.Zone)
	if e != nil || !clock.MatchString(j.Time) || j.Zone == "" || j.Zone == "Local" {
		return time.Time{}, errors.New("Choose a valid IANA timezone and HH:MM time")
	}
	end := after.Add(9 * 24 * time.Hour)
	for at := after.UTC().Truncate(time.Minute).Add(time.Minute); at.Before(end); at = at.Add(time.Minute) {
		v := at.In(loc)
		if v.Format("15:04") == j.Time && (j.Frequency == "daily" || (j.Frequency == "weekly" && int(v.Weekday()) == j.Weekday)) && Slot(j, at) != lastSlot {
			return at, nil
		}
	}
	return time.Time{}, errors.New("No schedule occurrence in the next nine days")
}
func Slot(j Job, t time.Time) string {
	loc, e := time.LoadLocation(j.Zone)
	if e != nil {
		return ""
	}
	return t.In(loc).Format("2006-01-02T15:04")
}
func parents(dir string) error {
	for p := dir; ; p = filepath.Dir(p) {
		if i, e := os.Lstat(p); e == nil && i.Mode()&os.ModeSymlink != 0 {
			return ErrState
		} else if e != nil && !os.IsNotExist(e) {
			return ErrState
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func Open(state string, now time.Time) (*Store, error) {
	dir := filepath.Join(state, "schedules")
	if !filepath.IsAbs(dir) || parents(dir) != nil {
		return nil, ErrState
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	i, e := os.Stat(dir)
	if e != nil || !i.IsDir() || i.Mode().Perm()&0077 != 0 {
		return nil, ErrState
	}
	s := &Store{file: filepath.Join(dir, "state.json"), data: disk{Version: 1, Jobs: []Job{}}}
	raw, e := s.read()
	if e != nil {
		return nil, e
	}
	if raw != nil {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&s.data) != nil || d.Decode(new(any)) != io.EOF || s.data.Version != 1 || len(s.data.Jobs) > MaxJobs {
			return nil, ErrState
		}
		s.existed = true
		s.hash = sha256.Sum256(raw)
	}
	seen := map[string]bool{}
	changed := false
	for n, j := range s.data.Jobs {
		if !valid(j) || seen[j.ID] {
			return nil, ErrState
		}
		seen[j.ID] = true
		if j.Last.Status == "running" {
			j.Last.Status = "unknown"
			j.Last.Finished = now
			j.Last.Message = "Interrupted. Inspect the host outcome before acknowledging this schedule."
			j.ReviewRequired = true
			j.Enabled = false
			changed = true
		}
		if !j.Next.After(now) {
			if !j.ReviewRequired {
				j.Last = Result{ID: newID(), Due: j.Next, Finished: now, Status: "skipped", Message: "Reef was unavailable at the scheduled time; no catch-up"}
			}
			j.LastSlot = Slot(j, j.Next)
			j.Next, e = Next(j, now, j.LastSlot)
			if e != nil {
				return nil, e
			}
			changed = true
		}
		s.data.Jobs[n] = j
	}
	if changed {
		if e = s.save(s.data); e != nil {
			return nil, e
		}
	}
	return s, nil
}
func (s *Store) read() ([]byte, error) {
	if i, e := os.Lstat(filepath.Dir(s.file)); e != nil || !i.IsDir() || i.Mode().Perm()&0077 != 0 {
		return nil, ErrState
	}
	if parents(filepath.Dir(s.file)) != nil {
		return nil, ErrState
	}
	i, e := os.Lstat(s.file)
	if os.IsNotExist(e) && !s.existed {
		return nil, nil
	}
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 || i.Size() > 2<<20 {
		return nil, ErrState
	}
	b, e := os.ReadFile(s.file)
	if e != nil {
		return nil, ErrState
	}
	if s.existed && sha256.Sum256(b) != s.hash {
		return nil, ErrState
	}
	return b, nil
}
func (s *Store) save(data disk) (result error) {
	defer func() {
		if result != nil {
			s.broken = true
		}
	}()
	if s.broken {
		return ErrState
	}
	if _, e := s.read(); e != nil {
		s.broken = true
		return e
	}
	b, e := json.Marshal(data)
	if e != nil || len(b) > 2<<20 {
		return ErrState
	}
	f, e := os.CreateTemp(filepath.Dir(s.file), ".schedule-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), s.file); e != nil {
		return e
	}
	s.data = data
	s.existed = true
	s.hash = sha256.Sum256(b)
	d, e := os.Open(filepath.Dir(s.file))
	if e != nil {
		s.broken = true
		return e
	}
	defer d.Close()
	if e = d.Sync(); e != nil {
		s.broken = true
	}
	return e
}
func (s *Store) List() ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken {
		return nil, ErrState
	}
	if _, e := s.read(); e != nil {
		s.broken = true
		return nil, e
	}
	return append([]Job{}, s.data.Jobs...), nil
}
func (s *Store) Change(j Job, expected uint64, reviewed bool, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := disk{Version: 1, Jobs: append([]Job{}, s.data.Jobs...)}
	at := -1
	for i, old := range data.Jobs {
		if old.ID == j.ID {
			at = i
			if old.Revision != expected {
				return Job{}, ErrChanged
			}
			if old.Last.Status == "running" {
				return Job{}, errors.New("Disable the running schedule or wait for completion before editing")
			}
			if old.ReviewRequired && !reviewed {
				return Job{}, errors.New("Inspect and acknowledge the uncertain host outcome first")
			}
			j.Last = old.Last
			j.LastSlot = old.LastSlot
			break
		}
	}
	if at < 0 {
		if expected != 0 || len(data.Jobs) >= MaxJobs {
			return Job{}, ErrChanged
		}
		if j.ID == "" {
			j.ID = newID()
		} else if !id.MatchString(j.ID) {
			return Job{}, ErrChanged
		}
		j.Last = Result{}
		j.LastSlot = ""
	}
	j.Revision = expected + 1
	j.ReviewRequired = false
	var e error
	j.Next, e = Next(j, now, j.LastSlot)
	if e != nil {
		return Job{}, e
	}
	if !valid(j) {
		return Job{}, errors.New("Invalid schedule, target, timezone or policy")
	}
	if at < 0 {
		data.Jobs = append(data.Jobs, j)
	} else {
		data.Jobs[at] = j
	}
	return j, s.save(data)
}
func (s *Store) Remove(id string, revision uint64, reviewed bool) error {
	return s.control(id, revision, "remove", reviewed)
}
func (s *Store) Disable(id string, revision uint64) error {
	return s.control(id, revision, "disable", false)
}
func (s *Store) control(id string, revision uint64, action string, reviewed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := disk{Version: 1, Jobs: append([]Job{}, s.data.Jobs...)}
	for i, j := range data.Jobs {
		if j.ID != id {
			continue
		}
		if j.Revision != revision {
			return ErrChanged
		}
		if action == "remove" {
			if j.Last.Status == "running" {
				return errors.New("Wait for the active run before deleting its schedule")
			}
			if j.ReviewRequired && !reviewed {
				return errors.New("Acknowledge the uncertain host outcome before deletion")
			}
			data.Jobs = append(data.Jobs[:i], data.Jobs[i+1:]...)
		} else {
			j.Enabled = false
			j.Revision++
			data.Jobs[i] = j
		}
		return s.save(data)
	}
	return ErrChanged
}

// Due returns metadata only. Claim performs the durable exactly-once admission.
func (s *Store) Due(now time.Time) ([]Job, error) {
	rows, e := s.List()
	if e != nil {
		return nil, e
	}
	out := []Job{}
	for _, j := range rows {
		if j.Enabled && !j.Next.After(now) {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Next.Before(out[j].Next) })
	return out, nil
}
func (s *Store) Claim(id string, revision uint64, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := disk{Version: 1, Jobs: append([]Job{}, s.data.Jobs...)}
	for i, j := range data.Jobs {
		if j.ID != id {
			continue
		}
		if j.Revision != revision || !j.Enabled || j.ReviewRequired || j.Next.After(now) || j.Last.Status == "running" {
			return Job{}, ErrChanged
		}
		due := j.Next
		j.LastSlot = Slot(j, due)
		next, e := Next(j, now, j.LastSlot)
		if e != nil {
			return Job{}, e
		}
		j.Next = next
		j.Last = Result{ID: newID(), Due: due, Started: now, Status: "running", Message: "Claimed before dispatch"}
		data.Jobs[i] = j
		return j, s.save(data)
	}
	return Job{}, ErrChanged
}
func (s *Store) Finish(id, run, status, message, operation string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch status {
	case "completed", "failed", "skipped", "unknown":
	default:
		return ErrChanged
	}
	data := disk{Version: 1, Jobs: append([]Job{}, s.data.Jobs...)}
	for i, j := range data.Jobs {
		if j.ID != id {
			continue
		}
		if j.Last.ID != run || j.Last.Status != "running" {
			return ErrChanged
		}
		j.Last.Status = status
		j.Last.Message = message
		j.Last.Operation = operation
		j.Last.Finished = now
		if status == "unknown" {
			j.Enabled = false
			j.ReviewRequired = true
		}
		if !valid(j) {
			return ErrState
		}
		data.Jobs[i] = j
		return s.save(data)
	}
	return ErrChanged
}
