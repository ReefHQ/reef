package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"reef/internal/agents"
	"reef/internal/history"
	"reef/internal/notifications"
)

func TestImageAvailabilityNotificationsUseVerifiedFreshReportsAndPreferPatchTags(t *testing.T) {
	m, p, _ := fixture(t, &fakeExecutor{})
	events := []notifications.Event{}
	m.Notify = func(e notifications.Event) { events = append(events, e) }
	row := ImageUpdate{Service: "web", Image: "example/app:1.0.0", Status: "available", ConfiguredFingerprint: strings.Repeat("a", 64)}
	report := ImageUpdates{Revision: "saved-revision", Services: []ImageUpdate{row}}
	m.publishImageUpdateNotices(p, report)
	if len(events) != 1 || !events[0].Valid() || events[0].Update.Kind != "image_digest" {
		t.Fatal(events)
	}
	row.Candidate = "example/app:1.0.1"
	row.VersionStatus = "available"
	row.CandidateFingerprint = strings.Repeat("b", 64)
	report.Services = []ImageUpdate{row}
	events = nil
	m.publishImageUpdateNotices(p, report)
	if len(events) != 1 || events[0].Update.Kind != "image_tag" || events[0].Update.Candidate != row.Candidate {
		t.Fatal(events)
	}
	firstReference := events[0].Operation
	row.ConfiguredFingerprint = strings.Repeat("c", 64)
	report.Services = []ImageUpdate{row}
	events = nil
	m.publishImageUpdateNotices(p, report)
	if len(events) != 1 || events[0].Operation == firstReference {
		t.Fatal("Republished configured tag was hidden by an unchanged patch candidate")
	}
	sameTargetReference := events[0].Operation
	row.Status = "current"
	report.Services = []ImageUpdate{row}
	events = nil
	m.publishImageUpdateNotices(p, report)
	if len(events) != 1 || events[0].Operation != sameTargetReference {
		t.Fatal("Installing current-tag content changed the candidate reference")
	}
	for _, kind := range []string{"error", "revision", "checking", "unverified", "unknown"} {
		r := report
		switch kind {
		case "error":
			r.Error = "Registry failed"
		case "revision":
			r.Revision = ""
		case "checking":
			r.Checking = true
		case "unknown":
			unknown := row
			unknown.Status = "unknown"
			r.Services = []ImageUpdate{unknown}
		case "unverified":
			unverified := row
			unverified.CandidateFingerprint = ""
			unverified.ConfiguredFingerprint = ""
			r.Services = []ImageUpdate{unverified}
		}
		events = nil
		m.publishImageUpdateNotices(p, r)
		if len(events) != 0 {
			t.Fatal(kind, events)
		}
	}
}
func TestImageCheckerPublishesAvailabilityWithoutMutatingDocker(t *testing.T) {
	m, p, _ := fixture(t, &fakeExecutor{})
	x := &imageExecutor{}
	m.Images = &ImageService{Exec: x}
	events := []notifications.Event{}
	m.Notify = func(e notifications.Event) { events = append(events, e) }
	if e := m.checkImages(p.ID, false); e != nil {
		t.Fatal(e)
	}
	m.wg.Wait()
	notices := 0
	for _, e := range events {
		if e.Scope == "update" {
			notices++
			if !e.Valid() || e.Update.Kind != "image_digest" {
				t.Fatal(e)
			}
		}
	}
	if notices != 1 {
		t.Fatal(events)
	}
	for _, bad := range []string{" pull", " up ", " down", "restart", "image rm"} {
		if strings.Contains(x.commands(), bad) {
			t.Fatal("Availability check mutated Docker", x.commands())
		}
	}
}
func TestManifestNoticeFingerprintIsStableAcrossReplicaOrderAndChangesWithRemoteContent(t *testing.T) {
	raw := `[{"Descriptor":{"platform":{"os":"linux","architecture":"amd64"}},"SchemaV2Manifest":{"config":{"digest":"` + oldImage + `"}}},{"Descriptor":{"platform":{"os":"linux","architecture":"arm64"}},"SchemaV2Manifest":{"config":{"digest":"` + newImage + `"}}}]`
	var manifests []registryManifest
	json.Unmarshal([]byte(raw), &manifests)
	amd, arm := imagePlatform{OS: "linux", Architecture: "amd64"}, imagePlatform{OS: "linux", Architecture: "arm64"}
	first, e := remoteManifestFingerprint(manifests, []imagePlatform{amd, arm, amd})
	second, _ := remoteManifestFingerprint(manifests, []imagePlatform{arm, amd})
	if e != nil || first != second || len(first) != 64 {
		t.Fatal(first, second, e)
	}
	manifests[0].SchemaV2Manifest.Config.Digest = "sha256:" + strings.Repeat("d", 64)
	changed, _ := remoteManifestFingerprint(manifests, []imagePlatform{amd, arm})
	if changed == first {
		t.Fatal("New content kept the old fingerprint")
	}
	if _, e = remoteManifestFingerprint(manifests, nil); e == nil {
		t.Fatal("No-platform notice accepted")
	}
}
func TestReefAvailabilityOnlyNotifiesFreshSuccessfulStableChecks(t *testing.T) {
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{}, "long-password-123456")
	s.webhooks = notificationQueue(t, false)
	s.webhooks.Configure(notifications.Config{Enabled: true, Format: "json", URL: "https://receiver.invalid", Categories: []string{"update"}}, false)
	s.reefUpdates.status.Current = "0.31.0"
	latest := "v0.32.0"
	status, calls := 200, 0
	s.reefUpdates.client = &http.Client{Transport: reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "" {
			t.Fatal(r)
		}
		return reefTagResponse(status, `[{"name":"`+latest+`"}]`), nil
	})}
	s.checkReefAndNotify(context.Background())
	if s.webhooks.View().Pending != 1 || s.webhooks.Batch()[0].Update.Latest != "0.32.0" {
		t.Fatal(s.webhooks.Batch())
	}
	s.webhooks.Ack([]string{s.webhooks.Batch()[0].ID})
	s.checkReefAndNotify(context.Background())
	if calls != 1 || s.webhooks.View().Pending != 0 {
		t.Fatal("Cooldown emitted an observation")
	}
	// Reset receiver tracking so deduplication cannot mask a stale-cache emission.
	if err := s.webhooks.Configure(notifications.Config{Enabled: true, Format: "json", URL: "https://another-receiver.invalid", Categories: []string{"update"}}, false); err != nil {
		t.Fatal(err)
	}
	s.reefUpdates.mu.Lock()
	s.reefUpdates.attempted = time.Time{}
	s.reefUpdates.mu.Unlock()
	status = 503
	s.checkReefAndNotify(context.Background())
	if s.webhooks.View().Pending != 0 || s.reefUpdates.snapshot().Status != "unknown" {
		t.Fatal("Stale cached availability was re-announced")
	}
	status = 200
	latest = "v0.33.0"
	s.reefUpdates.mu.Lock()
	s.reefUpdates.attempted = time.Time{}
	s.reefUpdates.mu.Unlock()
	s.checkReefAndNotify(context.Background())
	if s.webhooks.View().Pending != 1 || s.webhooks.Batch()[0].Update.Latest != "0.33.0" {
		t.Fatal(s.webhooks.Batch())
	}
}
func TestRemoteUpdateNoticesBindHostAndRequireNegotiation(t *testing.T) {
	s, _ := historyServer(t)
	s.webhooks.Configure(notifications.Config{Enabled: true, Format: "json", URL: "https://receiver.invalid", Categories: []string{"update"}}, false)
	token, _ := s.agents.Token("Remote")
	id, key, _ := s.agents.Enroll(token, agents.Protocol)
	gen, _ := s.agents.Begin(id, key, "0.31.0", agents.Protocol)
	s.agents.Control(id, gen, true)
	s.agents.Events(id, gen, true)
	event := notifications.NewUpdate(notifications.UpdateDetails{Kind: "image_digest", ProjectID: "project", Project: "Media", Service: "web", Image: "example/app:latest", Fingerprint: strings.Repeat("a", 64)})
	post := func(e notifications.Event) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"generation": gen, "events": []notifications.Event{e}})
		r := httptest.NewRequest("POST", "/agent/v1/events", bytes.NewReader(raw))
		r.Header.Set("X-Reef-Host", id)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := post(event); w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	s.agents.UpdateNotices(id, gen, true)
	for i := 0; i < 2; i++ {
		e := event
		e.ID = randomID()
		if w := post(e); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if s.webhooks.View().Pending != 1 || s.webhooks.Batch()[0].HostID != id {
		t.Fatal(s.webhooks.Batch())
	}
	page, e := s.history.Query(history.Query{Host: id, Scope: "update"})
	if e != nil || len(page.Rows) != 1 || page.Rows[0].Update.Service != "web" || !strings.Contains(page.Rows[0].Note, "no update was applied") {
		t.Fatal(page, e)
	}
	if w := post(notifications.ReefUpdate("0.31.0", "0.32.0")); w.Code != 400 {
		t.Fatal("Remote forged Reef announcement", w.Code)
	}
	s.agents.Control(id, gen, false)
	s.agents.Control(id, gen, true)
	s.agents.Events(id, gen, true)
	if s.agents.CanUpdateNotices(id) {
		t.Fatal("Update notice capability survived disable")
	}
}
func TestStartupImageChecksNotifyWithoutOpeningTheInterface(t *testing.T) {
	m, _, _ := fixture(t, &fakeExecutor{})
	m.Images = &ImageService{Exec: &imageExecutor{}}
	available := make(chan notifications.Event, 1)
	m.Notify = func(e notifications.Event) {
		if e.Scope == "update" {
			available <- e
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.startImageChecks(ctx)
	defer func() { cancel(); m.wg.Wait() }()
	select {
	case e := <-available:
		if !e.Valid() {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		cancel()
		m.wg.Wait()
		t.Fatal("Background check did not announce availability")
	}
	cancel()
	m.wg.Wait()
}
