package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func imageNotice(fp string) Event {
	return NewUpdate(UpdateDetails{Kind: "image_digest", ProjectID: "project", Project: "Media", Service: "web", Image: "example/app:latest", Fingerprint: strings.Repeat(fp, 64)})
}
func configureNotices(t *testing.T, s *Service, categories []string) {
	t.Helper()
	if err := s.Configure(Config{Enabled: true, Format: "json", URL: "https://receiver.invalid/fixture", Categories: categories}, false); err != nil {
		t.Fatal(err)
	}
}
func TestUpdateNoticesPersistDeduplicationAndRecognizeNewContent(t *testing.T) {
	s := fixture(t)
	configureNotices(t, s, []string{"update"})
	first := imageNotice("a")
	if !first.Valid() {
		t.Fatal(first)
	}
	s.Emit(first)
	s.Emit(imageNotice("a"))
	if s.View().Pending != 1 || len(s.data.UpdateNotices) != 1 {
		t.Fatal(s.View())
	}
	s.Ack([]string{first.ID})
	s, err := Open(filepath.Dir(filepath.Dir(s.file)), false)
	if err != nil {
		t.Fatal(err)
	}
	s.Emit(imageNotice("a"))
	if s.View().Pending != 0 {
		t.Fatal("Restart repeated an accepted notice")
	}
	s.Emit(imageNotice("b"))
	if s.View().Pending != 1 {
		t.Fatal("Republished latest was not detected")
	}
	s.Ack([]string{s.Batch()[0].ID})
	reef := ReefUpdate("0.31.0", "0.32.0")
	s.Emit(reef)
	s.Ack([]string{reef.ID})
	s.Emit(ReefUpdate("0.31.1", "0.32.0"))
	if s.View().Pending != 0 {
		t.Fatal("Installed version changed but target was already announced")
	}
	s.Emit(ReefUpdate("0.31.1", "0.33.0"))
	if s.View().Pending != 1 {
		t.Fatal(s.View())
	}
	if imageNotice("a").Operation != imageNotice("a").Operation || imageNotice("a").Operation == imageNotice("b").Operation {
		t.Fatal("Unstable update correlation")
	}
}
func TestFilteredAvailabilityCanBeNotifiedOnANewFreshObservation(t *testing.T) {
	s := fixture(t)
	configureNotices(t, s, []string{})
	old := imageNotice("a")
	if err := s.Accept("remote", "Remote", []Event{old}); err != nil {
		t.Fatal(err)
	}
	if len(s.data.UpdateNotices) != 0 || s.View().Pending != 0 {
		t.Fatal("Excluded notice marked as announced")
	}
	configureNotices(t, s, []string{"update"})
	if err := s.Accept("remote", "Remote", []Event{old}); err != nil || s.View().Pending != 0 {
		t.Fatal("Transport replay was delivered", err)
	}
	fresh := imageNotice("a")
	if err := s.Accept("remote", "Remote", []Event{fresh, imageNotice("a")}); err != nil || s.View().Pending != 1 {
		t.Fatal(err, s.View())
	}
	if err := s.Accept("other-host", "Another", []Event{imageNotice("a")}); err != nil || s.View().Pending != 2 {
		t.Fatal("Host-scoped announcements collapsed", err)
	}
	if err := s.Accept("remote", "Remote", []Event{ReefUpdate("0.31.0", "0.32.0")}); err == nil {
		t.Fatal("Remote spoofed central Reef notice")
	}
}
func TestUpdateNoticeMarkersAreAtomicWithQueuePersistence(t *testing.T) {
	s := fixture(t)
	configureNotices(t, s, []string{"update"})
	file := s.file
	s.file = filepath.Join(t.TempDir(), "missing", "state.json")
	s.Emit(imageNotice("a"))
	if len(s.data.UpdateNotices) != 0 || s.View().Pending != 0 {
		t.Fatal("Failed persistence suppressed future notice")
	}
	s.file = file
	s.Emit(imageNotice("a"))
	if s.View().Pending != 1 {
		t.Fatal(s.View())
	}
	s.Ack([]string{s.Batch()[0].ID})
	configureNotices(t, s, []string{})
	configureNotices(t, s, []string{"update"})
	s.Emit(imageNotice("a"))
	if s.View().Pending != 0 {
		t.Fatal("Category toggle repeated a notice")
	}
	if err := s.Configure(Config{Enabled: true, Format: "json", URL: "https://another.invalid", Categories: []string{"update"}}, false); err != nil {
		t.Fatal(err)
	}
	s.Emit(imageNotice("a"))
	if s.View().Pending != 1 {
		t.Fatal("New receiver did not get a fresh observation")
	}
}
func TestRelayAvailabilityOutboxIsBoundedAndFitsTheExistingProtocol(t *testing.T) {
	s, err := Open(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		d := imageNotice("a").Update
		d.Kind = "image_tag"
		d.ProjectID = fmt.Sprintf("p-%d", i)
		d.Project = strings.Repeat("😀", 120)
		d.Service = strings.Repeat("s", 128)
		d.Image = "repo/" + strings.Repeat("a", 500) + ":1.0.0"
		d.Candidate = "repo/" + strings.Repeat("a", 500) + ":1.0.1"
		e := NewUpdate(d)
		if !e.Valid() {
			t.Fatal("Invalid large synthetic event")
		}
		s.Emit(e)
		s.Emit(NewUpdate(d))
	}
	if len(s.data.Pending) != 32 {
		t.Fatal("Offline observations duplicated")
	}
	batch := s.Batch()
	wire, _ := json.Marshal(map[string]any{"generation": strings.Repeat("a", 64), "events": batch})
	if len(batch) == 0 || len(batch) >= 32 || len(wire) > 65536 {
		t.Fatal("Event batch exceeds transport budget", len(batch), len(wire))
	}
	reopened, err := Open(filepath.Dir(filepath.Dir(s.file)), true)
	if err != nil || len(reopened.Batch()) != len(batch) {
		t.Fatal(err)
	}
}
func TestUpdateMetadataValidationAndSharedCards(t *testing.T) {
	first := imageNotice("a")
	for _, bad := range []string{"https://user:secret@host/app", "/private/file"} {
		e := first
		e.Update.Image = bad
		if e.Valid() {
			t.Fatal("Unsafe metadata accepted")
		}
	}
	e := first
	e.Update.Service = strings.Repeat("x", 129)
	if e.Valid() {
		t.Fatal("Unbounded service")
	}
	e = first
	e.Operation = randomTestID()
	if e.Valid() {
		t.Fatal("Forged correlation accepted")
	}
	legacy := New("project", "start", "completed", "", "app")
	raw, _ := json.Marshal(legacy)
	if strings.Contains(string(raw), `"update"`) {
		t.Fatal("Legacy payload changed", string(raw))
	}
	for _, notice := range []Event{first, ReefUpdate("0.31.0", "0.32.0")} {
		raw, _, _ := payload(Config{Format: "json", URL: "https://receiver.invalid"}, notice)
		discord, _, _ := payload(Config{Format: "discord", URL: "https://discord.com/api/webhooks/123/fixture"}, notice)
		var generic struct {
			Event        Event
			Presentation eventPresentation
		}
		var target struct{ Embeds []eventPresentation }
		json.Unmarshal(raw, &generic)
		json.Unmarshal(discord, &target)
		if generic.Event != notice || !reflect.DeepEqual(generic.Presentation, target.Embeds[0]) || fieldValue(generic.Presentation, "Status") != "Update available" {
			t.Fatal(string(raw))
		}
		if notice.Update.Kind == "reef" && !strings.HasPrefix(generic.Presentation.URL, "https://github.com/ReefHQ/reef/blob/v0.32.0/") {
			t.Fatal(generic.Presentation.URL)
		}
	}
}
func randomTestID() string { return strings.Repeat("d", 48) }
func TestUpdateTrackingRejectsCorruptStateAndNeverBlocksRemoteAcknowledgementAtCapacity(t *testing.T) {
	s := fixture(t)
	configureNotices(t, s, []string{"update"})
	s.data.UpdateNotices = map[string]string{}
	for i := 0; i < MaxUpdateStreams; i++ {
		s.data.UpdateNotices[updateSum(fmt.Sprint(i))] = updateSum("seen")
	}
	if err := s.Accept("remote", "Remote", []Event{imageNotice("a")}); err != nil {
		t.Fatal(err)
	}
	if s.View().Pending != 0 || s.View().Dropped != 1 || len(s.data.Seen) != 1 {
		t.Fatal(s.View())
	}
	raw, _ := os.ReadFile(s.file)
	var data disk
	json.Unmarshal(raw, &data)
	data.UpdateNotices = map[string]string{"invalid": "not-a-fingerprint"}
	raw, _ = json.Marshal(data)
	os.WriteFile(s.file, raw, 0600)
	if _, err := Open(filepath.Dir(filepath.Dir(s.file)), false); err == nil {
		t.Fatal("Corrupt tracking state accepted")
	}
}

type noticeTransport func(*http.Request) (*http.Response, error)

func (f noticeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestUpdateNoticesRetryOnFreshObservationAfterFailedOrCancelledDelivery(t *testing.T) {
	s := fixture(t)
	configureNotices(t, s, []string{"update"})
	s.Emit(imageNotice("a"))
	configureNotices(t, s, []string{})
	configureNotices(t, s, []string{"update"})
	s.Emit(imageNotice("a"))
	if s.View().Pending != 1 {
		t.Fatal("Cancelled pending notice was permanently suppressed")
	}
	status := 400
	client := &http.Client{Transport: noticeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fixture"))}, nil
	})}
	s.SendOne(context.Background(), client)
	if s.View().Pending != 0 || len(s.data.UpdateNotices) != 0 {
		t.Fatal(s.View())
	}
	s.Emit(imageNotice("a"))
	if s.View().Pending != 1 {
		t.Fatal("Failed delivery suppressed all future observations")
	}
	status = 204
	s.SendOne(context.Background(), client)
	s.Emit(imageNotice("a"))
	if s.View().Pending != 0 || len(s.data.UpdateNotices) != 1 {
		t.Fatal("Confirmed availability repeated", s.View())
	}
}
