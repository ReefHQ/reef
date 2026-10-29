package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"reef/internal/schedules"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in. Central scheduling is exercised through the actual app APIs
// against only this test's uniquely named volume and consumer container.
func TestDockerScheduledBackupIntegration(t *testing.T) {
	if os.Getenv("REEF_TEST_SCHEDULES") != "1" {
		t.Skip("opt-in disposable scheduled backup fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	name := "reef-schedule-test-" + fmt.Sprint(time.Now().UnixNano())
	consumer := name + "-app"
	defer func() {
		_ = exec.Command("docker", "rm", "--force", consumer).Run()
		_ = exec.Command("docker", "volume", "rm", name).Run()
	}()
	docker := func(args ...string) string {
		t.Helper()
		b, e := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if e != nil {
			t.Fatal(args, e, string(b))
		}
		return strings.TrimSpace(string(b))
	}
	docker("volume", "create", name)
	docker("run", "--detach", "--name", consumer, "--network", "none", "--mount", "type=volume,src="+name+",dst=/data", "--entrypoint", "sh", backupHelperImage, "-c", `printf scheduled-backup-marker > /data/marker; trap 'exit 0' TERM; while :; do sleep 1; done`)
	s, u, _, _, _ := scheduleServer(t)
	x := catalogIntegrationExecutor{t}
	s.m.Exec = x
	s.m.Images = &ImageService{Exec: x}
	s.m.initVolumeBackups(Config{StateDir: t.TempDir()})
	if s.m.Backups.err != nil {
		t.Fatal(s.m.Backups.err)
	}
	defer s.m.Backups.store.Close()
	rows, e := s.m.Images.volumes(ctx)
	if e != nil {
		t.Fatal(e)
	}
	fp := ""
	for _, r := range rows {
		if r.Name == name {
			fp = r.Fingerprint
		}
	}
	j, e := s.schedules.Change(schedules.Job{Name: "Fixture backup", Host: "local", Action: "volume_backup", Target: name, Fingerprint: fp, StopConsumers: true, Owner: u.ID, OwnerRevision: u.Revision, Enabled: true, Frequency: "daily", Time: "03:00", Zone: "Europe/Paris"}, 0, false, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	s.scheduleTick(ctx, j.Next)
	s.m.wg.Wait()
	jobs, e := s.schedules.List()
	if e != nil || len(jobs) != 1 || jobs[0].Last.Status != "completed" || jobs[0].Last.Operation == "" {
		t.Fatal(jobs, e)
	}
	archives, e := s.m.Backups.store.List()
	if e != nil || len(archives) != 1 || archives[0].Status != "succeeded" {
		t.Fatal(archives, e)
	}
	if _, e = s.m.Backups.store.Archive(archives[0]); e != nil {
		t.Fatal(e)
	}
	if docker("inspect", "--format", "{{.State.Running}}", consumer) != "true" {
		t.Fatal("scheduled backup did not restart its consumer")
	}
	if docker("exec", consumer, "cat", "/data/marker") != "scheduled-backup-marker" {
		t.Fatal("source data changed")
	}
	s.scheduleTick(ctx, j.Next)
	s.m.wg.Wait()
	archives, _ = s.m.Backups.store.List()
	if len(archives) != 1 {
		t.Fatal("scheduled slot replayed", archives)
	}
	t.Log("Verified central schedule claim, real backup API dispatch, clean stop, private verified archive, consumer restart, source preservation and no duplicate slot; only owned fixtures removed")
}
