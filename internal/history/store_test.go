package history

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reef/internal/notifications"
)

var testHost = strings.Repeat("a", 64)

func openStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}
func event(action, status, operation string) notifications.Event {
	return notifications.New("project", action, status, operation, "example")
}
func drain(s *Store) { ctx, cancel := context.WithCancel(context.Background()); cancel(); s.Run(ctx) }
func TestHistoryPersistsGroupsOutcomesAndRecoversUnfinishedLocalWork(t *testing.T) {
	s, dir := openStore(t)
	operation := strings.Repeat("1", 48)
	start := event("apply", "started", operation)
	end := event("apply", "partial", operation)
	end.Count = 2
	end.Succeeded = 1
	end.Failed = 1
	s.RecordLocal(start)
	s.RecordLocal(end)
	s.RecordLocal(event("save", "started", strings.Repeat("2", 48)))
	drain(s)
	page, err := s.Query(Query{Host: "local"})
	if err != nil || len(page.Rows) != 2 {
		t.Fatal(page, err)
	}
	for _, row := range page.Rows {
		if row.Operation == operation && (row.Status != "partial" || row.Succeeded != 1 || row.Failed != 1) {
			t.Fatal(row)
		}
	}
	restarted, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = restarted.Query(Query{Host: "local", Status: "unknown"})
	if len(page.Rows) != 1 || !strings.Contains(page.Rows[0].Note, "restarted") {
		t.Fatal(page)
	}
	page, _ = restarted.Query(Query{Host: "all", Status: "partial"})
	if len(page.Rows) != 1 {
		t.Fatal(page)
	}
	info, err := os.Stat(filepath.Join(dir, "history", "actions.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
}
func TestRemoteHistoryBindsHostDeduplicatesAcrossRestartAndKeepsTerminalOutcome(t *testing.T) {
	s, dir := openStore(t)
	op := strings.Repeat("1", 48)
	start := event("stop", "started", op)
	end := event("stop", "completed", op)
	start.HostID = "forged-local"
	start.Host = "Forged"
	if err := s.Accept(testHost, "Remote", []notifications.Event{end, start}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(testHost, "Remote", []notifications.Event{start, end}); err != nil {
		t.Fatal(err)
	}
	page, _ := s.Query(Query{Host: testHost})
	if len(page.Rows) != 1 || page.Rows[0].Status != "completed" || page.Rows[0].Host != "Remote" || page.RetainedEvents != 2 {
		t.Fatal(page)
	}
	restarted, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.Accept(testHost, "Remote", []notifications.Event{end, start}); err != nil {
		t.Fatal(err)
	}
	page, _ = restarted.Query(Query{Host: testHost})
	if page.RetainedEvents != 2 {
		t.Fatal("replay duplicated history", page)
	}
	if page, _ = restarted.Query(Query{Host: "local"}); page.Total != 0 {
		t.Fatal("forged host accepted", page)
	}
	bad := notifications.New("webhook", "test", "completed", "", "forged")
	if s.Accept(testHost, "Remote", []notifications.Event{bad}) == nil || s.Accept("not-a-host", "Remote", nil) == nil {
		t.Fatal("invalid events accepted")
	}
}
func TestHistoryPaginationUsesStableSequenceSnapshotAndFilters(t *testing.T) {
	s, _ := openStore(t)
	for i := 0; i < 3; i++ {
		batch := []notifications.Event{}
		for n := 0; n < 32; n++ {
			e := event("save", "completed", "")
			e.Target = "database"
			batch = append(batch, e)
		}
		if err := s.Accept(testHost, "Remote", batch); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.Query(Query{Host: "all", Scope: "project", Status: "completed", Search: "DATABASE"})
	if err != nil || first.Total != 96 || len(first.Rows) != 50 {
		t.Fatal(first, err)
	}
	if err = s.Accept(testHost, "Remote", []notifications.Event{event("stop", "completed", "")}); err != nil {
		t.Fatal(err)
	}
	second, err := s.Query(Query{Host: "all", Cursor: first.Cursor, Offset: 50})
	if err != nil || second.Total != 96 || len(second.Rows) != 46 {
		t.Fatal(second, err)
	}
	seen := map[string]bool{}
	for _, r := range first.Rows {
		seen[r.ID] = true
	}
	for _, r := range second.Rows {
		if seen[r.ID] {
			t.Fatal("pagination overlapped")
		}
	}
	for _, q := range []Query{{Host: "../state"}, {Scope: "invalid"}, {Status: "invalid"}, {Search: strings.Repeat("x", 513)}, {Offset: 1}, {Offset: -50}, {Offset: 2050}, {Cursor: "x"}, {Cursor: "999999"}} {
		if _, err = s.Query(q); err == nil {
			t.Fatal("invalid query accepted", q)
		}
	}
}
func TestHistoryRetentionAndQueueLossAreBounded(t *testing.T) {
	s, _ := openStore(t)
	for i := 0; i < MaxEvents/32+2; i++ {
		batch := []notifications.Event{}
		for n := 0; n < 32; n++ {
			batch = append(batch, event("save", "completed", ""))
		}
		if err := s.Accept(testHost, "Remote", batch); err != nil {
			t.Fatal(err)
		}
	}
	page, _ := s.Query(Query{Host: "all"})
	if page.RetainedEvents != MaxEvents {
		t.Fatal(page.RetainedEvents)
	}
	s.mu.Lock()
	for i := range s.data.Entries {
		s.data.Entries[i].Recorded = time.Now().Add(-Retention - time.Hour)
	}
	if s.saveLocked() != nil {
		t.Fatal("cannot prepare aged fixture")
	}
	s.mu.Unlock()
	page, _ = s.Query(Query{Host: "all"})
	if page.Total != 0 || page.RetainedEvents != 0 {
		t.Fatal(page)
	}
	for i := 0; i < QueueSize+3; i++ {
		s.RecordLocal(event("save", "completed", ""))
	}
	page, _ = s.Query(Query{Host: "all"})
	if page.Dropped != 3 {
		t.Fatal("queue overflow not reported", page.Dropped)
	}
	drain(s)
}
func TestHistoryUnsafeAndExternallyChangedStateDoesNotGetOverwritten(t *testing.T) {
	for _, mode := range []string{"corrupt", "permissions", "symlink", "ancestor"} {
		t.Run(mode, func(t *testing.T) {
			s, dir := openStore(t)
			s.RecordLocal(event("save", "completed", ""))
			drain(s)
			file := filepath.Join(dir, "history", "actions.json")
			switch mode {
			case "corrupt":
				os.WriteFile(file, []byte("private-invalid-state"), 0600)
			case "permissions":
				os.Chmod(file, 0644)
			case "symlink":
				os.Remove(file)
				os.Symlink(filepath.Join(dir, "missing"), file)
			case "ancestor":
				os.Rename(filepath.Join(dir, "history"), filepath.Join(dir, "saved"))
				os.Symlink(filepath.Join(dir, "saved"), filepath.Join(dir, "history"))
			}
			if _, err := Open(dir); err == nil {
				t.Fatal("unsafe state opened")
			}
			err := s.Accept(testHost, "Remote", []notifications.Event{event("stop", "completed", "")})
			if err == nil {
				t.Fatal("unsafe state overwritten")
			}
			page, _ := s.Query(Query{Host: "all"})
			if !page.PersistenceError {
				t.Fatal(page)
			}
			// Action admission never waits for the failed history persistence path.
			for i := 0; i < QueueSize+1; i++ {
				s.RecordLocal(event("save", "completed", ""))
			}
			drain(s)
		})
	}
}
func TestHistoryStoreContainsOnlySelectedMetadataAndOptionalFailureIsReadable(t *testing.T) {
	s, dir := openStore(t)
	s.RecordLocal(event("save", "completed", ""))
	drain(s)
	raw, err := os.ReadFile(filepath.Join(dir, "history", "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(raw, &data) != nil {
		t.Fatal("invalid persisted state")
	}
	for _, key := range []string{"url", "config", "csrf", "password", "logs", "environment"} {
		if _, ok := data[key]; ok {
			t.Fatal("private field persisted", key)
		}
	}
	unavailable := Unavailable()
	unavailable.RecordLocal(event("stop", "completed", ""))
	page, err := unavailable.Query(Query{Host: "all"})
	if err != nil || !page.PersistenceError || page.Dropped != 1 {
		t.Fatal(page, err)
	}
}

func TestHistoryWebhookTestRecordsQueueAcceptanceNotDelivery(t *testing.T) {
	s, _ := openStore(t)
	s.RecordLocal(notifications.New("webhook", "test", "completed", "", "Test notification"))
	drain(s)
	page, err := s.Query(Query{Host: "local", Scope: "webhook"})
	if err != nil || len(page.Rows) != 1 || !strings.Contains(page.Rows[0].Note, "queued") {
		t.Fatal(page, err)
	}
}
