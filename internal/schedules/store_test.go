package schedules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, e := time.Parse(time.RFC3339, s)
	if e != nil {
		panic(e)
	}
	return t
}
func policy() Job {
	return Job{Name: "Nightly check", Host: "local", Owner: strings.Repeat("a", 64), OwnerRevision: 1, Action: "image_check", Target: "project", Fingerprint: strings.Repeat("b", 64), Frequency: "daily", Time: "02:30", Zone: "Europe/Paris", Enabled: true}
}
func TestNextAcrossClockChanges(t *testing.T) {
	cases := []struct {
		name, now, last, expected string
		j                         Job
	}{
		{"spring gap", "2026-03-28T03:00:00Z", "", "2026-03-30T00:30:00Z", policy()},
		{"fall first occurrence", "2026-10-24T03:00:00Z", "", "2026-10-25T00:30:00Z", policy()},
		{"fall duplicate skipped", "2026-10-25T00:30:00Z", "2026-10-25T02:30", "2026-10-26T01:30:00Z", policy()},
		{"weekly spring gap", "2026-03-28T03:00:00Z", "", "2026-04-05T00:30:00Z", func() Job { j := policy(); j.Frequency = "weekly"; j.Weekday = 0; return j }()},
		{"UTC boundary", "2026-10-29T23:59:40Z", "", "2026-10-30T00:00:00Z", func() Job { j := policy(); j.Zone = "UTC"; j.Time = "00:00"; return j }()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			next, e := Next(c.j, at(c.now), c.last)
			if e != nil || !next.Equal(at(c.expected)) {
				t.Fatal(next, e)
			}
		})
	}
}
func TestDurableClaimUnknownRestartAndNoCatchup(t *testing.T) {
	root := t.TempDir()
	now := at("2026-10-28T23:00:00Z")
	s, e := Open(root, now)
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Change(policy(), 0, false, now)
	if e != nil {
		t.Fatal(e)
	}
	due := j.Next
	first, e := s.Claim(j.ID, j.Revision, due)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Claim(j.ID, j.Revision, due); e == nil {
		t.Fatal("slot claimed twice")
	}
	reopened, e := Open(root, due.Add(time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	rows, _ := reopened.List()
	r := rows[0]
	if r.Enabled || !r.ReviewRequired || r.Last.Status != "unknown" || r.Last.ID != first.Last.ID {
		t.Fatal(r)
	}
	if _, e = reopened.Change(r, r.Revision, false, due.Add(time.Minute)); e == nil {
		t.Fatal("uncertainty ignored")
	}
	r.Enabled = true
	r, e = reopened.Change(r, r.Revision, true, due.Add(time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	if !r.Next.After(due) || r.ReviewRequired {
		t.Fatal("old slot replayed", r)
	}
	// No startup dispatch even when the old rendezvous is only a few seconds old.
	future := r.Next.Add(10 * time.Second)
	again, e := Open(root, future)
	if e != nil {
		t.Fatal(e)
	}
	rows, _ = again.List()
	if rows[0].Last.Status != "skipped" || !rows[0].Next.After(future) {
		t.Fatal(rows)
	}
}
func TestStaleEditsDisableDuringRunAndFinishIdentity(t *testing.T) {
	now := at("2026-10-29T00:00:00Z")
	s, e := Open(t.TempDir(), now)
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Change(policy(), 0, false, now)
	if e != nil {
		t.Fatal(e)
	}
	run, e := s.Claim(j.ID, j.Revision, j.Next)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Change(j, j.Revision, false, now); e == nil {
		t.Fatal("running schedule edited")
	}
	if e = s.Disable(j.ID, j.Revision); e != nil {
		t.Fatal(e)
	}
	if e = s.Finish(j.ID, "wrong", "completed", "done", "", now); e == nil {
		t.Fatal("foreign run result accepted")
	}
	if e = s.Finish(j.ID, run.Last.ID, "completed", "done", "", now); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.List()
	if rows[0].Enabled || rows[0].Last.Status != "completed" {
		t.Fatal(rows)
	}
	if e = s.Remove(j.ID, j.Revision, false); e == nil {
		t.Fatal("stale removal accepted")
	}
}
func TestInvalidPoliciesAndExternalState(t *testing.T) {
	now := at("2026-10-29T00:00:00Z")
	root := t.TempDir()
	s, e := Open(root, now)
	if e != nil {
		t.Fatal(e)
	}
	for _, edit := range []func(*Job){func(j *Job) { j.Zone = "Not/A_Zone" }, func(j *Job) { j.Time = "24:00" }, func(j *Job) { j.Action = "shell" }, func(j *Job) {
		j.Action = "image_cleanup"
		j.Target = ""
		j.Fingerprint = ""
		j.AgeDays = 0
		j.MaxItems = 100
	}, func(j *Job) { j.Action = "volume_backup"; j.StopConsumers = false }, func(j *Job) { j.Host = "--host" }} {
		j := policy()
		edit(&j)
		if _, e = s.Change(j, 0, false, now); e == nil {
			t.Fatal("invalid policy accepted", j)
		}
	}
	j, e := s.Change(policy(), 0, false, now)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(root, "schedules", "state.json")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, append(b, ' '), 0600)
	if _, e = s.Claim(j.ID, j.Revision, j.Next); e == nil {
		t.Fatal("externally changed state claimed")
	}
	if _, e = s.List(); e == nil {
		t.Fatal("broken scheduler did not fail closed")
	}
	other := t.TempDir()
	os.Mkdir(filepath.Join(other, "schedules"), 0700)
	os.Symlink(p, filepath.Join(other, "schedules", "state.json"))
	if _, e = Open(other, now); e == nil {
		t.Fatal("symlink accepted")
	}
}

func TestCreationDraftIdentityPreventsDuplicateAfterLostResponse(t *testing.T) {
	now := at("2026-10-29T00:00:00Z")
	s, e := Open(t.TempDir(), now)
	if e != nil {
		t.Fatal(e)
	}
	j := policy()
	j.ID = strings.Repeat("f", 48)
	first, e := s.Change(j, 0, false, now)
	if e != nil {
		t.Fatal(e)
	}
	if first.ID != j.ID {
		t.Fatal("creation identity replaced")
	}
	if _, e = s.Change(j, 0, false, now); e == nil {
		t.Fatal("creation replay accepted")
	}
	rows, e := s.List()
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
}
