package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reef/internal/agents"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type volumeExecutor struct {
	mu            sync.Mutex
	rows          []volumeMetadata
	used          map[string]bool
	calls         [][]string
	fail          bool
	gate, entered chan struct{}
}

func volumeFixture() *volumeExecutor {
	return &volumeExecutor{rows: []volumeMetadata{{Name: "app-data", Driver: "local", Scope: "local", Created: "2026-10-27T12:00:00Z", Mountpoint: "/docker/volumes/app-data/_data", Labels: map[string]string{"com.docker.compose.project": "app", "private": "private-label-secret"}, Options: map[string]string{"password": "private-driver-secret"}}, {Name: "old-data", Driver: "local", Scope: "local", Created: "2026-10-26T12:00:00Z"}}, used: map[string]bool{}}
}
func (x *volumeExecutor) Run(ctx context.Context, p Project, args []string, w io.Writer) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.calls = append(x.calls, append([]string{}, args...))
	if x.fail {
		io.WriteString(w, "private-cli-secret")
		return errors.New("private-cli-secret")
	}
	emit := func(value any) error { return json.NewEncoder(w).Encode(value) }
	switch strings.Join(args[:2], " ") {
	case "volume ls":
		for _, row := range x.rows {
			io.WriteString(w, row.Name+"\n")
		}
		return nil
	case "volume inspect":
		for _, name := range args[4:] {
			found := false
			for _, row := range x.rows {
				if row.Name == name {
					emit(row)
					found = true
					break
				}
			}
			if !found {
				return errors.New("not found")
			}
		}
		return nil
	case "container ls":
		for _, used := range x.used {
			if used {
				io.WriteString(w, oldContainer+"\n")
				break
			}
		}
		return nil
	case "container inspect":
		mounts := []map[string]string{}
		for name, used := range x.used {
			if used {
				mounts = append(mounts, map[string]string{"type": "volume", "name": name}, map[string]string{"type": "volume", "name": name})
			}
		}
		mounts = append(mounts, map[string]string{"type": "bind", "name": "old-data"})
		return emit(map[string]any{"id": oldContainer, "name": "/stopped-db", "mounts": mounts})
	case "volume create":
		if x.entered != nil {
			close(x.entered)
			select {
			case <-x.gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		name := args[len(args)-1]
		for _, row := range x.rows {
			if row.Name == name {
				return nil
			}
		}
		identity := strings.TrimPrefix(args[5], "io.reefhq.created=")
		x.rows = append(x.rows, volumeMetadata{Name: name, Driver: "local", Scope: "local", Created: "2026-10-27T13:00:00Z", Labels: map[string]string{"io.reefhq.created": identity}})
		return nil
	case "volume rm":
		name := args[2]
		if x.used[name] {
			return errors.New("in use")
		}
		for i, row := range x.rows {
			if row.Name == name {
				x.rows = append(x.rows[:i], x.rows[i+1:]...)
				return nil
			}
		}
		return errors.New("missing")
	}
	return errors.New("unexpected command")
}
func volumeTargets(t *testing.T, s *ImageService) []VolumeTarget {
	t.Helper()
	rows, err := s.volumes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := []VolumeTarget{}
	for _, row := range rows {
		out = append(out, VolumeTarget{row.Name, row.Fingerprint})
	}
	return out
}
func TestVolumesInventoryProtectsStoppedUsageAndOmitsSecrets(t *testing.T) {
	x := volumeFixture()
	x.used["app-data"] = true
	s := &ImageService{Exec: x}
	rows, err := s.volumes(context.Background())
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	if !reflect.DeepEqual(rows[0].Containers, []string{"stopped-db"}) || rows[0].Removable || !rows[1].Removable {
		t.Fatal(rows)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "private-") {
		t.Fatal("driver options or labels leaked")
	}
	if err := s.removeVolume(context.Background(), VolumeTarget{rows[0].Name, rows[0].Fingerprint}); err == nil {
		t.Fatal("used volume removed")
	}
	if err := s.removeVolume(context.Background(), VolumeTarget{rows[1].Name, rows[1].Fingerprint}); err != nil {
		t.Fatal(err)
	}
	for _, args := range x.calls {
		for _, arg := range args {
			if arg == "prune" || arg == "--force" || arg == "-f" {
				t.Fatal(args)
			}
		}
	}
}
func TestVolumeIdentityChangesAndMissingCreationIdentityRetainData(t *testing.T) {
	for _, mode := range []string{"created", "options", "missing"} {
		t.Run(mode, func(t *testing.T) {
			x := volumeFixture()
			s := &ImageService{Exec: x}
			target := volumeTargets(t, s)[0]
			switch mode {
			case "created":
				x.rows[0].Created = "2026-10-27T13:00:00Z"
			case "options":
				x.rows[0].Options["password"] = "changed-private-secret"
			case "missing":
				x.rows[0].Created = ""
				target = volumeTargets(t, s)[0]
			}
			if err := s.removeVolume(context.Background(), target); err == nil {
				t.Fatal("changed volume deleted")
			}
			for _, args := range x.calls {
				if args[1] == "rm" {
					t.Fatal(args)
				}
			}
		})
	}
}
func TestVolumesCreateAndBatchMutationsShareImageComposeLock(t *testing.T) {
	x := volumeFixture()
	x.gate = make(chan struct{})
	x.entered = make(chan struct{})
	m, p, rev := fixture(t, &fakeExecutor{})
	m.Images = &ImageService{Exec: x}
	if _, err := m.beginVolumes("create", "new-data", ""); err != nil {
		t.Fatal(err)
	}
	<-x.entered
	if _, err := m.beginCatalog("pull", "app:latest", "", ""); err == nil {
		t.Fatal("image overlapped volume worker")
	}
	if err := m.begin(p.ID, "stop", rev, ""); err == nil {
		t.Fatal("Compose overlapped volume worker")
	}
	if _, err := m.beginVolumes("create", "another-data", ""); err == nil {
		t.Fatal("duplicate volume worker")
	}
	close(x.gate)
	m.wg.Wait()
	op := m.Images.volumeOperation()
	if op.Running || len(op.Results) != 1 || !op.Results[0].OK {
		t.Fatal(op)
	}
	result := m.Images.runVolumes(context.Background(), "create", "new-data", nil)
	if result[0].OK {
		t.Fatal("existing volume reused")
	}
	targets := volumeTargets(t, m.Images)
	raw, _ := json.Marshal(targets)
	if _, err := m.beginVolumes("cleanup", "", string(raw)); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	if len(x.rows) != 0 {
		t.Fatal(x.rows)
	}
	for _, name := range []string{"--help", "../data", "/data", "with spaces", "a", strings.Repeat("x", 256)} {
		if _, err := m.beginVolumes("create", name, ""); err == nil {
			t.Fatal("invalid name", name)
		}
	}
}
func TestVolumeMutationCannotOverlapImageWorker(t *testing.T) {
	x := catalogFixture()
	x.gate = make(chan struct{})
	x.entered = make(chan struct{})
	m, _, _ := fixture(t, &fakeExecutor{})
	m.Images = &ImageService{Exec: x}
	m.beginCatalog("pull", "app:latest", "", "")
	<-x.entered
	if _, err := m.beginVolumes("create", "new-data", ""); err == nil {
		t.Fatal("volume overlapped image worker")
	}
	close(x.gate)
	m.wg.Wait()
}
func TestVolumeFailuresAreSanitizedAndInvalidBatchesRejected(t *testing.T) {
	x := volumeFixture()
	s := &ImageService{Exec: x}
	targets := volumeTargets(t, s)
	x.fail = true
	for _, action := range []string{"create", "remove"} {
		results := s.runVolumes(context.Background(), action, "new-data", targets)
		for _, result := range results {
			if result.OK || strings.Contains(result.Message, "secret") {
				t.Fatal(results)
			}
		}
	}
	m, _, _ := fixture(t, &fakeExecutor{})
	m.Images = s
	for _, raw := range []string{"[]", "{}", `[{"name":"--force","fingerprint":"bad"}]`} {
		if _, err := m.beginVolumes("cleanup", "", raw); err == nil {
			t.Fatal("invalid batch accepted")
		}
	}
	raw, _ := json.Marshal([]VolumeTarget{targets[0], targets[0]})
	if _, err := m.beginVolumes("cleanup", "", string(raw)); err == nil {
		t.Fatal("duplicate targets accepted")
	}
}
func TestVolumesAPISecurityAndRemoteIsolation(t *testing.T) {
	local, _, _ := fixture(t, &fakeExecutor{})
	lx := volumeFixture()
	local.Images = &ImageService{Exec: lx}
	central := newServer(local, Config{AgentsEnabled: true}, "password-long-enough")
	central.agents, _ = agents.Open(t.TempDir())
	central.sessions[sessionKey("cookie")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "cookie"}
	remote, _, _ := fixture(t, &fakeExecutor{})
	rx := volumeFixture()
	rx.rows[0].Name = "remote-data"
	remote.Images = &ImageService{Exec: rx}
	backend := newServer(remote, Config{}, "unused-password")
	backend.discovery = &inventoryStub{}
	legacy, stop := relayAgentFixture(t, central, backend, "old-agent", agents.ImagesCapability)
	defer stop()
	h := central.Handler()
	if w := call(h, "GET", "/api/volumes?host="+legacy, "", "", cookie); w.Code != 409 {
		t.Fatal(w.Code)
	}
	host, stopReady := relayAgentFixture(t, central, backend, "capable-agent", agents.VolumesCapability)
	defer stopReady()
	for _, tc := range []struct {
		method, path, body, origin string
		cookie                     *http.Cookie
		status                     int
	}{
		{"GET", "/api/volumes", "", "", nil, 401}, {"GET", "/api/volumes/action", "", "", cookie, 405},
		{"POST", "/api/volumes/action", "csrf=csrf&confirmed=yes&action=create&name=new-data", "http://evil.test", cookie, 403},
		{"POST", "/api/volumes/action", "csrf=wrong&confirmed=yes&action=create&name=new-data", "http://reef.test", cookie, 403},
		{"POST", "/api/volumes/action", "csrf=csrf&action=create&name=new-data", "http://reef.test", cookie, 400},
	} {
		w := call(h, tc.method, tc.path, tc.body, tc.origin, tc.cookie)
		if w.Code != tc.status {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	w := call(h, "GET", "/api/volumes?host="+host, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "remote-data") || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	body := url.Values{"csrf": {"csrf"}, "confirmed": {"yes"}, "action": {"create"}, "name": {"remote-created"}}.Encode()
	w = call(h, "POST", "/api/volumes/action?host="+host, body, "http://reef.test", cookie)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	remote.wg.Wait()
	if len(lx.calls) > 0 {
		t.Fatal("remote request touched local Docker")
	}
	w = call(h, "GET", "/api/volumes/operation?host="+host, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Completed") {
		t.Fatal(w.Code, w.Body.String())
	}
}
