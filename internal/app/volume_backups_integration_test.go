package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type backupFailureExecutor struct {
	base Executor
	fail string
}

func (x backupFailureExecutor) Run(c context.Context, p Project, a []string, w io.Writer) error {
	return x.Output(c, p, a, w)
}
func (x backupFailureExecutor) Output(c context.Context, p Project, a []string, w io.Writer) error {
	if x.fail == "stop" && len(a) > 1 && a[0] == "container" && a[1] == "stop" {
		return errors.New("injected stop failure")
	}
	if x.fail == "restart" && len(a) == 3 && a[0] == "container" && a[1] == "start" {
		return errors.New("injected restart failure")
	}
	if x.fail == "export" && len(a) > 3 && a[0] == "container" && a[1] == "start" && a[2] == "--attach" {
		return errors.New("injected export failure")
	}
	return x.base.(interface {
		Output(context.Context, Project, []string, io.Writer) error
	}).Output(c, p, a, w)
}
func TestDockerVolumeBackupsIntegration(t *testing.T) {
	if os.Getenv("REEF_TEST_BACKUPS") != "1" {
		t.Skip("opt-in owned Docker volume backup fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	name := "reef-backup-test-" + fmt.Sprint(time.Now().UnixNano())
	dest := name + "-restored"
	active := name + "-active"
	stopped := name + "-stopped"
	docker := func(a ...string) string {
		t.Helper()
		raw, e := exec.CommandContext(ctx, "docker", a...).CombinedOutput()
		if e != nil {
			t.Fatalf("docker %v: %v %s", a, e, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	defer func() {
		for _, c := range []string{active, stopped} {
			_ = exec.Command("docker", "rm", "--force", c).Run()
		}
		for _, v := range []string{name, dest} {
			_ = exec.Command("docker", "volume", "rm", v).Run()
		}
	}()
	docker("volume", "create", name)
	docker("run", "--rm", "--network", "none", "--mount", "type=volume,src="+name+",dst=/data", "--entrypoint", "sh", backupHelperImage, "-c", `printf reef-backup-marker > /data/marker; mkdir /data/private; printf private-marker > /data/private/secret; chown -R 1234:1235 /data/private; chmod 000 /data/private/secret; chmod 0700 /data/private; ln /data/marker /data/hard; ln -s /data/marker /data/link`)
	docker("run", "--detach", "--name", active, "--network", "none", "--mount", "type=volume,src="+name+",dst=/data", "--entrypoint", "sh", backupHelperImage, "-c", `trap 'exit 0' TERM; while :; do sleep 1; done`)
	docker("create", "--name", stopped, "--network", "none", "--mount", "type=volume,src="+name+",dst=/data", "--entrypoint", "true", backupHelperImage)
	x := catalogIntegrationExecutor{t}
	c := Config{StateDir: t.TempDir(), VolumeBackupRetention: 1}
	m := &Manager{Exec: x, Images: &ImageService{Exec: x}}
	m.initVolumeBackups(c)
	if m.Backups.err != nil {
		t.Fatal(m.Backups.err)
	}
	defer m.Backups.store.Close()
	rows, e := m.Images.volumes(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var fp string
	for _, r := range rows {
		if r.Name == name {
			fp = r.Fingerprint
		}
	}
	preview := func() backupPreview {
		t.Helper()
		p, e := m.Images.backupPreview(ctx, name, fp)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	p := preview()
	if len(p.Containers) != 2 {
		t.Fatal(p)
	}
	if e = m.runVolumeBackup(ctx, "backup", name, fp, p.Usage, "", ""); e != nil {
		t.Fatal(e)
	}
	if v := docker("inspect", "--format", "{{.State.Running}}", active); v != "true" {
		t.Fatal("active container not resumed")
	}
	if v := docker("inspect", "--format", "{{.State.Running}}", stopped); v != "false" {
		t.Fatal("stopped container started")
	}
	records, e := m.Backups.store.List()
	if e != nil || len(records) != 1 || records[0].Status != "succeeded" {
		t.Fatal(records, e)
	}
	saved := records[0]
	if e = m.runVolumeBackup(ctx, "restore", "", "", "", saved.ID, name); e == nil {
		t.Fatal("existing volume accepted")
	}
	if e = m.runVolumeBackup(ctx, "restore", "", "", "", saved.ID, dest); e != nil {
		t.Fatal(e)
	}
	data := docker("run", "--rm", "--network", "none", "--mount", "type=volume,src="+dest+",dst=/data,readonly", "--entrypoint", "sh", backupHelperImage, "-c", `cat /data/marker; cat /data/private/secret; stat -c '%u:%g:%a' /data/private/secret; stat -c '%u:%g:%a' /data/private; readlink /data/link; test /data/marker -ef /data/hard`)
	if data != "reef-backup-markerprivate-marker1234:1235:0\n1234:1235:700\n/data/marker" {
		t.Fatalf("restored data/ownership/modes/links changed: %q", data)
	}
	for _, failure := range []string{"stop", "export", "restart"} {
		t.Run(failure, func(t *testing.T) {
			bad := backupFailureExecutor{base: x, fail: failure}
			m.Exec = bad
			m.Images.Exec = bad
			p := preview()
			if e = m.runVolumeBackup(ctx, "backup", name, fp, p.Usage, "", ""); e == nil {
				t.Fatal("injected failure ignored")
			}
			m.Exec = x
			m.Images.Exec = x
			expected := "true"
			if failure == "restart" {
				expected = "false"
			}
			if docker("inspect", "--format", "{{.State.Running}}", active) != expected {
				t.Fatal("unexpected application state after injected failure")
			}
			if failure == "restart" {
				docker("start", active)
			}
			records, e := m.Backups.store.List()
			if e != nil || records[0].Status != "failed" {
				t.Fatal(records, e)
			}
		})
	}
	// A changed review token must not stop a container or begin an export.
	if e = m.runVolumeBackup(ctx, "backup", name, fp, strings.Repeat("0", 64), "", ""); e == nil {
		t.Fatal("stale review accepted")
	}
	t.Log("Verified archive export and restore, content, numeric ownership, mode 000/0700, symlinks/hardlinks, active-only restart, stop/export/restart failures, stale review and existing-volume refusal; fixture resources cleaned")
}
