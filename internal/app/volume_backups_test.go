package app

import (
	"context"
	"net/http"
	"reef/internal/agents"
	"reef/internal/volumebackup"
	"strings"
	"testing"
	"time"
)

func backupStoreFixture(t *testing.T, m *Manager, name string) volumebackup.Record {
	t.Helper()
	m.initVolumeBackups(Config{StateDir: t.TempDir()})
	if m.Backups.err != nil {
		t.Fatal(m.Backups.err)
	}
	t.Cleanup(func() { m.Backups.store.Close() })
	r := volumebackup.Record{ID: randomID(), Volume: name, Fingerprint: strings.Repeat("a", 64), Status: "failed", Created: time.Now(), Containers: []string{}, Message: "Incomplete archive"}
	if e := m.Backups.store.Save(r); e != nil {
		t.Fatal(e)
	}
	return r
}
func TestBackupAPISecurityAndRemoteHostIsolation(t *testing.T) {
	central, _ := historyServer(t)
	local := backupStoreFixture(t, central.m, "local-only")
	central.m.Images = &ImageService{Exec: volumeFixture()}
	rm, _, _ := fixture(t, &fakeExecutor{})
	backupStoreFixture(t, rm, "remote-only")
	rm.Images = &ImageService{Exec: volumeFixture()}
	remote := newServer(rm, Config{}, "unused")
	remote.discovery = &inventoryStub{}
	legacy, stop := relayAgentFixture(t, central, remote, "legacy", agents.VolumesCapability)
	defer stop()
	host, disconnect := relayAgentFixture(t, central, remote, "ready", agents.VolumesCapability, agents.VolumeBackupsCapability)
	defer disconnect()
	cookie := &http.Cookie{Name: "qcu_session", Value: "cookie"}
	h := central.Handler()
	for _, tc := range []struct {
		method, path, body, origin string
		cookie                     *http.Cookie
		status                     int
	}{
		{"GET", "/api/volume-backups", "", "", nil, 401},
		{"GET", "/api/volume-backups?host=" + legacy, "", "", cookie, 409},
		{"POST", "/api/volume-backups", "csrf=csrf&confirmed=yes&action=delete&backup=" + local.ID, "http://evil.test", cookie, 403},
		{"POST", "/api/volume-backups", "csrf=wrong&confirmed=yes&action=delete&backup=" + local.ID, "http://reef.test", cookie, 403},
		{"POST", "/api/volume-backups", "csrf=csrf&action=delete&backup=" + local.ID, "http://reef.test", cookie, 400},
	} {
		w := call(h, tc.method, tc.path, tc.body, tc.origin, tc.cookie)
		if w.Code != tc.status {
			t.Fatalf("%s %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	w := call(h, "GET", "/api/volume-backups?host="+host, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "remote-only") || strings.Contains(w.Body.String(), "local-only") {
		t.Fatal(w.Code, w.Body.String())
	}
	// A valid central-local record cannot be deleted on a remote host.
	w = call(h, "POST", "/api/volume-backups?host="+host, "csrf=csrf&confirmed=yes&action=delete&backup="+local.ID, "http://reef.test", cookie)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	rm.wg.Wait()
	if rm.Backups.operation().Results[0].OK {
		t.Fatal("foreign backup accepted")
	}
	rows, e := central.m.Backups.store.List()
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
}
func TestInterruptedBackupRecoveryAndStorageExclusion(t *testing.T) {
	m, _, _ := fixture(t, &fakeExecutor{})
	c := Config{StateDir: t.TempDir()}
	m.initVolumeBackups(c)
	if m.Backups.err != nil {
		t.Fatal(m.Backups.err)
	}
	r := volumebackup.Record{ID: randomID(), Volume: "database", Fingerprint: strings.Repeat("a", 64), Status: "pending", Created: time.Now(), Containers: []string{strings.Repeat("b", 64)}, Message: "Stopping"}
	if e := m.Backups.store.Save(r); e != nil {
		t.Fatal(e)
	}
	m.Backups.store.Close()
	m.initVolumeBackups(c)
	defer m.Backups.store.Close()
	rows, e := m.Backups.store.List()
	if e != nil || len(rows) != 1 || rows[0].Status != "interrupted" || len(rows[0].Containers) != 1 {
		t.Fatal(rows, e)
	}
	m.Images = &ImageService{Exec: volumeFixture()}
	m.storageBusy = true
	if _, e = m.beginVolumeBackup("delete", "", "", "", r.ID, ""); e == nil {
		t.Fatal("overlapping storage operation admitted")
	}
	m.storageBusy = false
	if _, e = m.beginVolumeBackup("restore", "", "", "", r.ID, "--bad"); e == nil {
		t.Fatal("invalid destination")
	}
	if e = m.runVolumeBackup(context.Background(), "restore", "", "", "", r.ID, "new-volume"); e == nil {
		t.Fatal("interrupted archive restored")
	}
}
