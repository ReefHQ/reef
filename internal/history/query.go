package history

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"reef/internal/notifications"
)

const PageSize = 50

var scopes = map[string]bool{"container": true, "schedule": true, "project": true, "image": true, "volume": true, "network": true, "build": true, "agent": true, "console": true, "webhook": true, "monitor": true}
var statuses = map[string]bool{"started": true, "completed": true, "failed": true, "partial": true, "closed": true, "unknown": true}

type Query struct {
	RestrictAdminEvents                 bool
	AllowedHosts                        map[string]bool
	Host, Scope, Status, Search, Cursor string
	Offset                              int
}
type Row struct {
	sequence  uint64
	ID        string    `json:"id"`
	HostID    string    `json:"host_id"`
	Host      string    `json:"host"`
	Scope     string    `json:"scope"`
	Action    string    `json:"action"`
	Status    string    `json:"status"`
	Operation string    `json:"operation_id,omitempty"`
	Target    string    `json:"target,omitempty"`
	Started   time.Time `json:"started"`
	Time      time.Time `json:"time"`
	Recorded  time.Time `json:"recorded"`
	Count     int       `json:"count,omitempty"`
	Succeeded int       `json:"succeeded,omitempty"`
	Failed    int       `json:"failed,omitempty"`
	Skipped   int       `json:"skipped,omitempty"`
	Note      string    `json:"note,omitempty"`
}
type Page struct {
	Rows             []Row     `json:"rows"`
	Total            int       `json:"total"`
	Offset           int       `json:"offset"`
	Limit            int       `json:"limit"`
	Cursor           string    `json:"cursor"`
	RetainedEvents   int       `json:"retained_events"`
	MaxEvents        int       `json:"max_events"`
	RetentionDays    int       `json:"retention_days"`
	Dropped          uint64    `json:"dropped"`
	PersistenceError bool      `json:"persistence_error"`
	Checked          time.Time `json:"checked_at"`
}

func groupID(e notifications.Event) string {
	operation := e.Operation
	if operation == "" {
		operation = e.ID
	}
	sum := sha256.Sum256([]byte(e.HostID + ":" + e.Scope + ":" + e.Action + ":" + operation))
	return hex.EncodeToString(sum[:])
}
func (s *Store) Query(q Query) (Page, error) {
	page := Page{Rows: []Row{}, Limit: PageSize, MaxEvents: MaxEvents, RetentionDays: 30, Checked: time.Now().UTC()}
	if q.Host == "" {
		q.Host = "local"
	}
	if (q.Host != "all" && q.Host != "local" && !hostID.MatchString(q.Host)) || (q.Scope != "" && !scopes[q.Scope]) || (q.Status != "" && !statuses[q.Status]) || len(q.Search) > 512 || q.Offset < 0 || q.Offset > MaxEvents || q.Offset%PageSize != 0 {
		return page, errors.New("Invalid history filters or page")
	}
	var through uint64
	var err error
	if q.Cursor != "" {
		through, err = strconv.ParseUint(q.Cursor, 10, 64)
		if err != nil {
			return page, errors.New("Invalid history cursor")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pruneLocked(page.Checked) {
		_ = s.saveLocked()
	}
	if q.Cursor == "" {
		through = s.data.Sequence
	} else if through > s.data.Sequence {
		return page, errors.New("History cursor is no longer available; refresh the latest page")
	}
	page.Cursor = strconv.FormatUint(through, 10)
	visible := func(e notifications.Event) bool {
		return (q.AllowedHosts == nil || q.AllowedHosts[e.HostID]) && (!q.RestrictAdminEvents || (e.Scope != "webhook" && !(e.Scope == "agent" && (e.Action == "enroll" || e.Action == "rename" || e.Action == "revoke"))))
	}
	page.RetainedEvents = 0
	for _, item := range s.data.Entries {
		if visible(item.Event) && (q.Host == "all" || q.Host == item.Event.HostID) {
			page.RetainedEvents++
		}
	}
	page.PersistenceError = s.persistenceError || !s.Ready()
	page.Dropped = s.dropped.Load()
	groups := map[string]entry{}
	started := map[string]time.Time{}
	for _, item := range s.data.Entries {
		e := item.Event
		if !visible(e) {
			continue
		}
		if item.Sequence > through || (q.Host != "all" && q.Host != e.HostID) {
			continue
		}
		id := groupID(e)
		if t := started[id]; t.IsZero() || e.Time.Before(t) {
			started[id] = e.Time
		}
		prior, ok := groups[id]
		// A late/replayed start must not overwrite a recorded terminal outcome.
		if !ok || (e.Status != "started" && prior.Event.Status == "started") || (e.Status == "started" && prior.Event.Status == "started" && item.Sequence > prior.Sequence) || (e.Status != "started" && prior.Event.Status != "started" && item.Sequence > prior.Sequence) {
			groups[id] = item
		}
	}
	search := strings.ToLower(strings.TrimSpace(q.Search))
	rows := []Row{}
	for id, item := range groups {
		e := item.Event
		status, note := e.Status, ""
		if e.Status == "started" {
			note = "Last recorded start; no final outcome received"
			if item.Recovered {
				status = "unknown"
				note = "Central restarted before a final result was recorded; inspect the host before retrying"
			}
		}
		if e.Scope == "monitor" {
			if e.Status == "failed" {
				note = "Monitoring incident opened"
			} else if e.Status == "completed" {
				note = "Monitoring recovery observed"
			}
		}
		if e.Scope == "webhook" && e.Action == "test" {
			note = "Notification request queued; delivery status is shown in Webhooks"
		}
		if (q.Scope != "" && q.Scope != e.Scope) || (q.Status != "" && q.Status != status) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(e.Host+" "+e.Target+" "+e.Scope+" "+e.Action+" "+e.Operation), search) {
			continue
		}
		rows = append(rows, Row{sequence: item.Sequence, ID: id, HostID: e.HostID, Host: e.Host, Scope: e.Scope, Action: e.Action, Status: status, Operation: e.Operation, Target: e.Target, Started: started[id], Time: e.Time, Recorded: item.Recorded, Count: e.Count, Succeeded: e.Succeeded, Failed: e.Failed, Skipped: e.Skipped, Note: note})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Recorded.Equal(rows[j].Recorded) {
			return rows[i].sequence > rows[j].sequence
		}
		return rows[i].Recorded.After(rows[j].Recorded)
	})
	page.Total = len(rows)
	if q.Offset >= len(rows) {
		q.Offset = 0
	}
	page.Offset = q.Offset
	page.Rows = rows[q.Offset:min(q.Offset+PageSize, len(rows))]
	return page, nil
}
