package monitoring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reef/internal/notifications"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Store, *time.Time, string) {
	t.Helper()
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Add(-2 * time.Hour)
	s.now = func() time.Time { return now }
	s.started = now
	c := Defaults()
	c.Enabled = true
	c.UnhealthySeconds = 30
	c.DisconnectSeconds = 60
	c.RecoverySeconds = 30
	if e = s.Configure(c); e != nil {
		t.Fatal(e)
	}
	return s, &now, dir
}
func row(health string, restarts int64) Container {
	return Container{ID: strings.Repeat("a", 64), Name: "database", State: "running", Health: health, Restarts: restarts}
}
func observe(s *Store, seq uint64, c ...Container) {
	s.Observe("local", "Local", Sample{Sequence: seq, Containers: c})
}
func TestUnhealthyRecoveryDedupRestartAndGaps(t *testing.T) {
	s, now, dir := fixture(t)
	observe(s, 1, row("unhealthy", 0))
	if len(s.View().Incidents) != 0 {
		t.Fatal("early alert")
	}
	*now = now.Add(30 * time.Second)
	observe(s, 2, row("unhealthy", 0))
	if len(s.Pending()) != 1 {
		t.Fatal(s.View())
	}
	id := s.Pending()[0].ID
	*now = now.Add(30 * time.Second)
	observe(s, 3, row("unhealthy", 0))
	if len(s.Pending()) != 1 {
		t.Fatal("duplicate opening")
	}
	reopened, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	s = reopened
	s.now = func() time.Time { return *now }
	s.started = *now
	observe(s, 1, row("unhealthy", 0))
	if len(s.Pending()) != 1 || s.Pending()[0].ID != id {
		t.Fatal("restart duplicated incident")
	}
	*now = now.Add(30 * time.Second)
	observe(s, 2, row("healthy", 0))
	*now = now.Add(30 * time.Second)
	s.Observe("local", "Local", Sample{Sequence: 3, Error: "Collection unavailable"})
	*now = now.Add(30 * time.Second)
	observe(s, 4, row("healthy", 0))
	if s.View().Incidents[0].Status != "firing" {
		t.Fatal("gap recovered")
	}
	*now = now.Add(30 * time.Second)
	observe(s, 5, row("healthy", 0))
	if s.View().Incidents[0].Status != "resolved" || len(s.Pending()) != 2 || s.Pending()[1].Operation != id {
		t.Fatal(s.View(), s.Pending())
	}
	*now = now.Add(30 * time.Second)
	observe(s, 6, row("healthy", 0))
	if len(s.Pending()) != 2 {
		t.Fatal("duplicate recovery")
	}
}
func TestRestartDeltasWindowResetAndStableRecovery(t *testing.T) {
	s, now, _ := fixture(t)
	observe(s, 1, row("healthy", 50))
	*now = now.Add(30 * time.Second)
	observe(s, 2, row("healthy", 52))
	if len(s.Pending()) != 0 {
		t.Fatal("below threshold")
	}
	*now = now.Add(30 * time.Second)
	observe(s, 3, row("healthy", 53))
	if len(s.Pending()) != 1 || s.Pending()[0].Action != "restart_loop" {
		t.Fatal(s.View())
	}
	for i := uint64(4); i <= 13; i++ {
		*now = now.Add(30 * time.Second)
		observe(s, i, row("healthy", 53))
	}
	if s.View().Incidents[0].Status != "firing" {
		t.Fatal("window recovered too early")
	}
	*now = now.Add(30 * time.Second)
	observe(s, 14, row("healthy", 53))
	*now = now.Add(30 * time.Second)
	observe(s, 15, row("healthy", 53))
	if s.View().Incidents[0].Status != "resolved" {
		t.Fatal(s.View())
	}
	*now = now.Add(30 * time.Second)
	observe(s, 16, row("healthy", 0))
	*now = now.Add(30 * time.Second)
	observe(s, 17, row("healthy", 1))
	if len(s.Pending()) != 2 {
		t.Fatal("counter reset counted as restart storm")
	}
}
func TestMaintenanceRemovalStoppedAndPolicyDoNotAnnounceRecovery(t *testing.T) {
	for _, mode := range []string{"maintenance", "removed", "stopped", "policy", "no-healthcheck", "row-error"} {
		t.Run(mode, func(t *testing.T) {
			s, now, _ := fixture(t)
			observe(s, 1, row("unhealthy", 0))
			*now = now.Add(30 * time.Second)
			observe(s, 2, row("unhealthy", 0))
			*now = now.Add(30 * time.Second)
			switch mode {
			case "maintenance":
				s.Observe("local", "Local", Sample{Sequence: 3, Maintenance: true})
			case "removed":
				observe(s, 3)
			case "stopped":
				c := row("unhealthy", 0)
				c.State = "exited"
				observe(s, 3, c)
			case "policy":
				c := s.View().Config
				c.Enabled = false
				if e := s.Configure(c); e != nil {
					t.Fatal(e)
				}
			case "no-healthcheck":
				observe(s, 3, row("", 0))
			case "row-error":
				c := row("healthy", 0)
				c.Error = true
				observe(s, 3, c)
			}
			for _, e := range s.Pending() {
				if e.Status == "completed" {
					t.Fatal("unobserved recovery", mode)
				}
			}
			if mode == "maintenance" || mode == "row-error" {
				if s.View().Incidents[0].Status != "firing" {
					t.Fatal(s.View())
				}
			} else if s.View().Incidents[0].Status != "retired" {
				t.Fatal(s.View())
			}
		})
	}
}
func TestDisconnectedStartupGraceMaintenanceRevocationAndRecovery(t *testing.T) {
	s, now, dir := fixture(t)
	id := strings.Repeat("b", 64)
	s.HostStatus(id, "remote", "disconnected", time.Time{}, false)
	if len(s.Pending()) != 0 {
		t.Fatal("immediate startup alarm")
	}
	*now = now.Add(60 * time.Second)
	s.HostStatus(id, "remote", "disconnected", time.Time{}, false)
	if len(s.Pending()) != 1 {
		t.Fatal(s.View())
	}
	s.HostStatus(id, "remote", "connected", *now, false)
	*now = now.Add(30 * time.Second)
	s.HostStatus(id, "remote", "connected", *now, false)
	if len(s.Pending()) != 2 || s.View().Incidents[0].Status != "resolved" {
		t.Fatal(s.View())
	}
	if e := s.Hold(id, "remote", now.Add(2*time.Minute)); e != nil {
		t.Fatal(e)
	}
	*now = now.Add(time.Minute)
	s.HostStatus(id, "remote", "disconnected", time.Time{}, false)
	if len(s.Pending()) != 2 {
		t.Fatal("maintenance alarm")
	}
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	s.now = func() time.Time { return *now }
	s.started = *now
	s.HostStatus(id, "remote", "disconnected", time.Time{}, false)
	if s.View().Hosts[0].Status != "maintenance" {
		t.Fatal("maintenance lost across restart")
	}
	*now = now.Add(2 * time.Minute)
	s.HostStatus(id, "remote", "disconnected", time.Time{}, false)
	if len(s.Pending()) != 3 {
		t.Fatal(s.View())
	}
	s.HostStatus(id, "remote", "revoked", time.Time{}, false)
	if s.View().Incidents[0].Status != "retired" || len(s.Pending()) != 3 {
		t.Fatal(s.View())
	}
}
func TestSequencesAndUnknownIntervalsNeverFastForwardTimers(t *testing.T) {
	s, now, _ := fixture(t)
	observe(s, 1, row("unhealthy", 0))
	*now = now.Add(30 * time.Second)
	observe(s, 1, row("unhealthy", 0))
	if len(s.Pending()) != 0 {
		t.Fatal("reused sample advanced timer")
	}
	*now = now.Add(2 * time.Minute)
	observe(s, 2, row("unhealthy", 0))
	if len(s.Pending()) != 0 {
		t.Fatal("stale gap counted")
	}
	*now = now.Add(30 * time.Second)
	observe(s, 3, row("unhealthy", 0))
	if len(s.Pending()) != 1 {
		t.Fatal(s.View())
	}
}
func TestPrivateStateExternalChangeBoundsAndFailedWrites(t *testing.T) {
	for _, mode := range []string{"corrupt", "public", "symlink", "external"} {
		t.Run(mode, func(t *testing.T) {
			s, now, dir := fixture(t)
			path := filepath.Join(dir, "monitoring", "state.json")
			switch mode {
			case "corrupt":
				os.WriteFile(path, []byte("{"), 0600)
			case "public":
				os.Chmod(path, 0644)
			case "symlink":
				os.Remove(path)
				os.Symlink(filepath.Join(dir, "elsewhere"), path)
			case "external":
				os.WriteFile(path, []byte("{}"), 0600)
			}
			observe(s, 1, row("unhealthy", 0))
			*now = now.Add(30 * time.Second)
			observe(s, 2, row("unhealthy", 0))
			if !s.View().PersistenceError || len(s.Pending()) != 0 {
				t.Fatal(s.View())
			}
			if _, e := Open(dir); e == nil {
				t.Fatal("unsafe state accepted")
			}
		})
	}
	s, _, _ := fixture(t)
	c := s.View().Config
	c.RestartCount = 1
	if s.Configure(c) == nil {
		t.Fatal("invalid config")
	}
	sample := Sample{Sequence: 1, Containers: []Container{row("healthy", 0), row("healthy", 0)}}
	if sample.Valid() {
		t.Fatal("duplicate ID")
	}
	sample.Containers = []Container{row("healthy", -1)}
	if sample.Valid() {
		t.Fatal("negative restart count")
	}
	if Unavailable().Configure(Defaults()) == nil {
		t.Fatal("unavailable state changed")
	}
}
func TestOutboxAckRetentionAndNoRawMetadata(t *testing.T) {
	s, now, _ := fixture(t)
	observe(s, 1, row("unhealthy", 0))
	*now = now.Add(30 * time.Second)
	observe(s, 2, row("unhealthy", 0))
	e := s.Pending()[0]
	if !e.Valid() {
		t.Fatal(e)
	}
	if err := s.Ack(e.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.Pending()) != 0 {
		t.Fatal("ack not removed")
	}
	*now = now.Add(30 * time.Second)
	observe(s, 3)
	*now = now.Add(31 * 24 * time.Hour)
	observe(s, 4)
	if len(s.View().Incidents) != 0 {
		t.Fatal("retention")
	}
	b, _ := json.Marshal(s.View())
	if strings.Contains(string(b), "RestartCount") || strings.Contains(string(b), "Env") {
		t.Fatal(string(b))
	}
}

func TestFullOutboxDelaysTransitionsAndKeepsExistingIncidents(t *testing.T) {
	s, now, _ := fixture(t)
	for i := 0; i < MaxPending; i++ {
		e := notifications.New("monitor", "unhealthy", "failed", "", "previous")
		e.Time = *now
		s.data.Pending = append(s.data.Pending, e)
	}
	observe(s, 1, row("unhealthy", 0))
	*now = now.Add(30 * time.Second)
	observe(s, 2, row("unhealthy", 0))
	if !s.View().CapacityLimited || len(s.View().Incidents) != 0 {
		t.Fatal(s.View())
	}
	if e := s.Ack(s.Pending()[0].ID); e != nil {
		t.Fatal(e)
	}
	*now = now.Add(30 * time.Second)
	observe(s, 3, row("unhealthy", 0))
	if len(s.View().Incidents) != 1 || s.View().CapacityLimited {
		t.Fatal(s.View())
	}
}
