package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reef/internal/accounts"
	"reef/internal/agents"
	"reef/internal/schedules"
	"strings"
	"sync"
	"testing"
	"time"
)

func scheduleServer(t *testing.T) (*Server, accounts.User, *http.Cookie, string, *catalogExecutor) {
	t.Helper()
	s, u, c, csrf := userServer(t)
	var e error
	s.schedules, e = schedules.Open(t.TempDir(), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	x := catalogFixture()
	s.m.Images = &ImageService{Exec: x}
	return s, u, c, csrf, x
}
func scheduledJob(t *testing.T, s *Server, u accounts.User, host string) schedules.Job {
	t.Helper()
	j, e := s.schedules.Change(schedules.Job{Name: "Old dangling images", Host: host, Action: "image_cleanup", Owner: u.ID, OwnerRevision: u.Revision, Frequency: "daily", Time: "03:00", Zone: "UTC", AgeDays: 1, MaxItems: 100, Enabled: true}, 0, false, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func TestSchedulesAPIConfirmationPermissionsAndNoMutationDuringConfiguration(t *testing.T) {
	s, u, c, csrf, x := scheduleServer(t)
	body := url.Values{"csrf": {csrf}, "confirmed": {"yes"}, "action": {"save"}, "id": {""}, "revision": {"0"}, "name": {"Old images"}, "kind": {"image_cleanup"}, "frequency": {"daily"}, "weekday": {"1"}, "time": {"03:00"}, "zone": {"Europe/Paris"}, "enabled": {"false"}, "age_days": {"7"}, "max_items": {"100"}, "stop_consumers": {"false"}}
	for _, tc := range []struct {
		method, path, body, origin string
		c                          *http.Cookie
		code                       int
	}{
		{"GET", "/api/schedules", "", "", nil, 401}, {"POST", "/api/schedules", body.Encode(), "http://evil.test", c, 403}, {"POST", "/api/schedules", strings.Replace(body.Encode(), "confirmed=yes", "confirmed=no", 1), "http://reef.test", c, 400},
		{"POST", "/api/schedules", strings.Replace(body.Encode(), "csrf="+csrf, "csrf=wrong", 1), "http://reef.test", c, 403}, {"POST", "/api/schedules", body.Encode(), "http://reef.test", c, 200},
	} {
		w := call(s.Handler(), tc.method, tc.path, tc.body, tc.origin, tc.c)
		if w.Code != tc.code {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	jobs, e := s.schedules.List()
	if e != nil || len(jobs) != 1 || jobs[0].Enabled || jobs[0].Owner != u.ID {
		t.Fatal(jobs, e)
	}
	for _, args := range x.calls {
		if strings.Contains(strings.Join(args, " "), "image rm") {
			t.Fatal("configuration mutated Docker")
		}
	}
	remote := strings.Repeat("c", 64)
	reader := addReader(t, s, "viewer", remote)
	cookie, _ := userCookie(t, s, reader)
	w := call(s.Handler(), "GET", "/api/schedules?host="+remote+"&all=true", "", "", cookie)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Old images") {
		t.Fatal("foreign schedule exposed", w.Code, w.Body.String())
	}
	w = call(s.Handler(), "GET", "/api/schedules", "", "", cookie)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	rcsrf := s.sessions[sessionKey(cookie.Value)].CSRF
	w = call(s.Handler(), "POST", "/api/schedules?host="+remote, "csrf="+rcsrf+"&confirmed=yes&action=delete&id="+jobs[0].ID+"&revision=1", "http://reef.test", cookie)
	if w.Code != 403 {
		t.Fatal("viewer changed schedules", w.Code)
	}
	if agents.AllowedCommand("GET", "/api/schedules") || agents.AllowedCommand("POST", "/api/schedules") {
		t.Fatal("central schedule configuration exposed to agents")
	}
}
func TestScheduledCleanupRunsOnceUnderExistingMutationGuards(t *testing.T) {
	s, u, _, _, x := scheduleServer(t)
	j := scheduledJob(t, s, u, "local")
	s.scheduleTick(context.Background(), j.Next)
	s.m.wg.Wait()
	jobs, e := s.schedules.List()
	if e != nil || jobs[0].Last.Status != "completed" {
		t.Fatal(jobs, e)
	}
	x.mu.Lock()
	remaining := append([]CatalogImage{}, x.rows...)
	before := len(x.calls)
	x.mu.Unlock()
	if len(remaining) != 1 || remaining[0].ID != oldImage {
		t.Fatal("named image affected", remaining)
	}
	s.scheduleTick(context.Background(), j.Next)
	s.m.wg.Wait()
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.calls) != before {
		t.Fatal("slot replayed")
	}
}
func TestScheduledRemoteCleanupStaysOnItsHost(t *testing.T) {
	s, u, _, _, local := scheduleServer(t)
	rm, _, _ := fixture(t, &fakeExecutor{})
	remoteExec := catalogFixture()
	remoteExec.rows = remoteExec.rows[1:]
	rm.Images = &ImageService{Exec: remoteExec}
	backend := newServer(rm, Config{}, "unused")
	backend.discovery = &inventoryStub{}
	host, stop := relayAgentFixture(t, s, backend, "Scheduled remote", agents.ImagesCapability, agents.ImageBatchCapability)
	defer stop()
	j := scheduledJob(t, s, u, host)
	s.scheduleTick(context.Background(), j.Next)
	s.m.wg.Wait()
	jobs, e := s.schedules.List()
	if e != nil || jobs[0].Last.Status != "completed" {
		t.Fatal(jobs, e)
	}
	if len(remoteExec.rows) != 0 || len(local.calls) != 0 {
		t.Fatal("remote action escaped its host")
	}
}

type scheduledGate struct {
	base    Executor
	once    sync.Once
	gate    chan struct{}
	entered chan struct{}
}

func (x *scheduledGate) Run(ctx context.Context, p Project, a []string, w io.Writer) error {
	x.once.Do(func() {
		close(x.entered)
		select {
		case <-ctx.Done():
		case <-x.gate:
		}
	})
	return x.base.Run(ctx, p, a, w)
}
func TestScheduleHostNonOverlapAndBusySkip(t *testing.T) {
	s, u, _, _, x := scheduleServer(t)
	one := scheduledJob(t, s, u, "local")
	two := scheduledJob(t, s, u, "local")
	gate := &scheduledGate{base: x, gate: make(chan struct{}), entered: make(chan struct{})}
	s.m.Images.Exec = gate
	s.scheduleTick(context.Background(), one.Next)
	close(gate.gate)
	s.m.wg.Wait()
	rows, _ := s.schedules.List()
	skipped := 0
	completed := 0
	for _, r := range rows {
		if r.Last.Status == "skipped" {
			skipped++
		}
		if r.Last.Status == "completed" {
			completed++
		}
	}
	if skipped != 1 || completed != 1 {
		t.Fatal("overlapping host dispatches", rows)
	}
	s.m.Images.Exec = x
	x.mu.Lock()
	x.rows = append(x.rows, CatalogImage{ID: newImage, Created: "2020-01-01T00:00:00Z", OS: "linux", Architecture: "amd64"})
	x.mu.Unlock()
	s.m.storageBusy = true
	s.scheduleTick(context.Background(), two.Next.Add(24*time.Hour))
	s.m.wg.Wait()
	s.m.storageBusy = false
	rows, _ = s.schedules.List()
	for _, r := range rows {
		if r.Last.Status == "unknown" {
			t.Fatal("known admission rejection marked uncertain")
		}
	}
}
func TestScheduleUncertainOutcomeBlocksHostAndRevokedAuthorizationDisables(t *testing.T) {
	s, u, _, _, _ := scheduleServer(t)
	one := scheduledJob(t, s, u, "local")
	run, e := s.schedules.Claim(one.ID, one.Revision, one.Next)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.schedules.Finish(one.ID, run.Last.ID, "unknown", "Review host", "", time.Now()); e != nil {
		t.Fatal(e)
	}
	two := scheduledJob(t, s, u, "local")
	s.scheduleTick(context.Background(), two.Next)
	s.m.wg.Wait()
	rows, _ := s.schedules.List()
	if rows[1].Last.Status != "skipped" {
		t.Fatal(rows)
	}
	if e = s.schedules.Remove(one.ID, one.Revision, false); e == nil {
		t.Fatal("unknown outcome discarded without acknowledgement")
	}
	if e = s.schedules.Remove(one.ID, one.Revision, true); e != nil {
		t.Fatal(e)
	}
	// Password/session revision changes revoke the persistent schedule grant too.
	if _, e = s.accounts.Change(u, "new-password-1234567", false); e != nil {
		t.Fatal(e)
	}
	s.scheduleTick(context.Background(), two.Next.Add(24*time.Hour))
	s.m.wg.Wait()
	rows, _ = s.schedules.List()
	if rows[0].Enabled || rows[0].Last.Status != "skipped" {
		t.Fatal(rows)
	}
}
func TestScheduleInternalIdentityRechecksAccountAndExactAction(t *testing.T) {
	s, u, _, _, x := scheduleServer(t)
	j := scheduledJob(t, s, u, "local")
	run, e := s.schedules.Claim(j.ID, j.Revision, j.Next)
	if e != nil {
		t.Fatal(e)
	}
	identity := scheduleIdentity{Session: session{UserID: u.ID, Revision: u.Revision, CSRF: randomID(), Expires: time.Now().Add(time.Hour)}, Job: run}
	if e = s.scheduleInvoke(context.Background(), identity, "local", "POST", "/api/action", url.Values{"action": {"stop"}, "id": {"app"}}, nil); e == nil {
		t.Fatal("schedule identity admitted unrelated action")
	}
	if e = s.schedules.Disable(j.ID, j.Revision); e != nil {
		t.Fatal(e)
	}
	if e = s.scheduleInvoke(context.Background(), identity, "local", "POST", "/api/images/action", url.Values{"action": {"cleanup"}, "targets": {"[]"}}, nil); e == nil {
		t.Fatal("disabled claim could mutate")
	}
	if len(x.calls) != 0 {
		t.Fatal("rejected identity reached Docker")
	}
}
func TestScheduleAgePoliciesKeepUnknownRecentTaggedAndSharedResources(t *testing.T) {
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	j := schedules.Job{AgeDays: 7, MaxItems: 1}
	fp := strings.Repeat("a", 64)
	rows := []CatalogImage{{ID: "old", Created: "2026-10-01T00:00:00Z", Fingerprint: fp}, {ID: "tagged", Created: "2026-09-01T00:00:00Z", Tags: []string{"app:latest"}}, {ID: "digest", Created: "2026-09-01T00:00:00Z", Digests: []string{"repo@sha256:..."}}, {ID: "recent", Created: "2026-10-28T00:00:00Z"}, {ID: "unknown"}, {ID: "used", Created: "2026-09-01T00:00:00Z", Containers: []string{"stopped"}}}
	selected := scheduledImages(rows, j, now)
	if len(selected) != 1 || selected[0].ID != "old" {
		t.Fatal(selected)
	}
	cache := []BuildCacheRecord{{ID: "cold", Created: "2026-10-01T00:00:00Z", LastUsed: "2026-10-02T00:00:00Z", Reclaimable: true}, {ID: "warm", Created: "2026-09-01T00:00:00Z", LastUsed: "2026-10-28T00:00:00Z", Reclaimable: true}, {ID: "shared", Created: "2026-09-01T00:00:00Z", Reclaimable: true, Shared: true}, {ID: "mutable", Created: "2026-09-01T00:00:00Z", Reclaimable: true, Mutable: true}}
	targets := scheduledCache(cache, j, now)
	if len(targets) != 1 || targets[0].ID != "cold" {
		t.Fatal(targets)
	}
	b, _ := json.Marshal(j)
	if strings.Contains(string(b), "password") {
		t.Fatal("secret in schedule policy")
	}
}

func TestScheduleAdmissionFailuresDistinguishUnknownMutations(t *testing.T) {
	for _, tc := range []struct {
		err      error
		mutation bool
		status   string
	}{{scheduleCallError{409}, true, "skipped"}, {scheduleCallError{403}, true, "skipped"}, {scheduleCallError{503}, true, "unknown"}, {context.Canceled, true, "unknown"}, {scheduleCallError{503}, false, "failed"}} {
		status, _ := scheduleFailure(tc.err, tc.mutation)
		if status != tc.status {
			t.Fatal(tc, status)
		}
	}
}
func TestSchedulesSkipOfflineMissedAndCapacitySlots(t *testing.T) {
	s, u, _, _, x := scheduleServer(t)
	offline := scheduledJob(t, s, u, strings.Repeat("c", 64))
	s.scheduleTick(context.Background(), offline.Next)
	s.m.wg.Wait()
	rows, _ := s.schedules.List()
	if rows[0].Last.Status != "skipped" || !strings.Contains(rows[0].Last.Message, "disconnected") {
		t.Fatal(rows)
	}
	late := scheduledJob(t, s, u, "local")
	s.scheduleTick(context.Background(), late.Next.Add(2*time.Minute))
	s.m.wg.Wait()
	rows, _ = s.schedules.List()
	if rows[1].Last.Status != "skipped" || !strings.Contains(rows[1].Last.Message, "missed") {
		t.Fatal(rows)
	}
	capacity := scheduledJob(t, s, u, "local")
	s.scheduleActive = map[string]bool{"one": true, "two": true, "three": true, "four": true}
	s.scheduleTick(context.Background(), capacity.Next)
	s.m.wg.Wait()
	rows, _ = s.schedules.List()
	if rows[2].Last.Status != "skipped" || !strings.Contains(rows[2].Last.Message, "capacity") {
		t.Fatal(rows)
	}
	if len(x.calls) != 0 {
		t.Fatal("skipped rendezvous reached Docker")
	}
}
