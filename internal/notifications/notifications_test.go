package notifications

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) *Service {
	t.Helper()
	s, err := Open(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestDiscordPayloadSuppressesMentionsAndUsesConfirmedDelivery(t *testing.T) {
	e := New("project", "apply", "completed", strings.Repeat("a", 48), "@everyone <@123> `private-name`")
	e.Host = "@here"
	b, target, err := payload(Config{Format: "discord", URL: "https://discord.com/api/webhooks/123/private-token"}, e)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Content string `json:"content"`
		Allowed struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if json.Unmarshal(b, &v) != nil || v.Allowed.Parse == nil || len(v.Allowed.Parse) != 0 || strings.Contains(v.Content, "@everyone") || len([]rune(v.Content)) > 2000 || !strings.Contains(target, "wait=true") {
		t.Fatal(string(b), target)
	}
}
func TestPrivateDestinationAndQueueSurviveRestartWithoutLeakingURL(t *testing.T) {
	s := fixture(t)
	secret := "https://receiver.invalid/private-token?token=another-secret"
	if err := s.Configure(Config{Enabled: true, Format: "json", URL: secret}, false); err != nil {
		t.Fatal(err)
	}
	s.Emit(New("volume", "create", "completed", "", "cache-data"))
	b, _ := json.Marshal(s.View())
	if strings.Contains(string(b), "private-token") || strings.Contains(string(b), "another-secret") {
		t.Fatal("Destination secret exposed")
	}
	info, err := os.Stat(s.file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	reopened, err := Open(filepath.Dir(filepath.Dir(s.file)), false)
	if err != nil || reopened.View().Pending != 1 || !reopened.View().Configured {
		t.Fatal(reopened, err)
	}
	if err = reopened.Configure(Config{Enabled: false, Format: "json"}, false); err != nil {
		t.Fatal(err)
	}
	if !reopened.View().Configured || reopened.View().Pending != 0 {
		t.Fatal(reopened.View())
	}
	if err = reopened.Configure(Config{Enabled: false, Format: "json"}, true); err != nil {
		t.Fatal(err)
	}
	if reopened.View().Configured {
		t.Fatal("Destination not removed")
	}
}
func TestGenericDeliveryAndRemoteDeduplicationBindAuthenticatedHost(t *testing.T) {
	var got Event
	var key string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Schema string `json:"schema"`
			Event  Event  `json:"event"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		got = payload.Event
		key = r.Header.Get("X-Reef-Event-ID")
		if payload.Schema != "reef.action.v1" {
			t.Error(payload.Schema)
		}
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	s := fixture(t)
	s.Configure(Config{Enabled: true, Format: "json", URL: receiver.URL}, false)
	e := New("project", "stop", "completed", strings.Repeat("a", 48), "web")
	e.HostID = "forged"
	e.Host = "forged name"
	if err := s.Accept("authenticated-id", "Ego", []Event{e}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept("authenticated-id", "Ego", []Event{e}); err != nil {
		t.Fatal(err)
	}
	if s.View().Pending != 1 {
		t.Fatal("Duplicate batch queued")
	}
	if !s.SendOne(context.Background(), receiver.Client()) || s.View().Pending != 0 || s.View().Sent != 1 {
		t.Fatal(s.View())
	}
	if got.HostID != "authenticated-id" || got.Host != "Ego" || key != "authenticated-id:"+e.ID {
		t.Fatal(got, key)
	}
	if err := s.Accept("another-id", "Other", []Event{e}); err != nil || s.View().Pending != 1 {
		t.Fatal("Host deduplication crossed identities", err)
	}
}
func TestDeliveryRespectsRateLimitsAndNeverFollowsRedirects(t *testing.T) {
	for _, status := range []int{429, 503, 302, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls, redirects := 0, 0
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects++; w.WriteHeader(204) }))
			defer target.Close()
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", target.URL)
				if status == 429 {
					w.Header().Set("Retry-After", "60")
				}
				w.WriteHeader(status)
				io.WriteString(w, `{"retry_after":30,"message":"private-token"}`)
			}))
			defer receiver.Close()
			s := fixture(t)
			s.Configure(Config{Enabled: true, Format: "json", URL: receiver.URL}, false)
			s.Emit(New("image", "pull", "started", "", "image:tag"))
			s.SendOne(context.Background(), receiver.Client())
			if redirects != 0 || strings.Contains(s.View().LastError, "private-token") {
				t.Fatal("Redirect followed or receiver body disclosed")
			}
			if status == 429 || status == 503 {
				if s.View().Pending != 1 || s.data.Pending[0].Next.Before(time.Now().Add(29*time.Second)) {
					t.Fatal("Retry delay ignored", s.View())
				}
				s.SendOne(context.Background(), receiver.Client())
				if calls != 1 {
					t.Fatal("Retried too early")
				}
			} else if s.View().Pending != 0 || s.View().Dropped != 1 {
				t.Fatal("Permanent failure retried", s.View())
			}
		})
	}
}
func TestRetryBudgetAndQueueBoundsDoNotBlockActions(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer receiver.Close()
	s := fixture(t)
	s.Configure(Config{Enabled: true, Format: "json", URL: receiver.URL}, false)
	s.Emit(New("build", "build", "failed", "", "web"))
	for i := 0; i < 5; i++ {
		s.data.After = time.Time{}
		s.data.Pending[0].Next = time.Time{}
		s.SendOne(context.Background(), receiver.Client())
	}
	if s.View().Pending != 0 || s.View().Dropped != 1 {
		t.Fatal(s.View())
	}
	e := New("image", "cleanup", "started", "", "")
	for i := 0; i < MaxPending; i++ {
		s.data.Pending = append(s.data.Pending, pending{Event: e})
	}
	s.Emit(e)
	if s.View().Pending != MaxPending || s.View().Dropped != 2 {
		t.Fatal(s.View())
	}
}
func TestRelayAcknowledgementIsDurableAndDoesNotStoreDestination(t *testing.T) {
	s, err := Open(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	e := New("console", "session", "closed", strings.Repeat("b", 48), "web")
	s.Emit(e)
	if len(s.Batch()) != 1 {
		t.Fatal("Relay dropped a disabled-central event")
	}
	if err = s.Ack([]string{strings.Repeat("c", 48)}); err != nil || len(s.Batch()) != 1 {
		t.Fatal("Unrelated acknowledgement removed event")
	}
	if err = s.Ack([]string{e.ID}); err != nil || len(s.Batch()) != 0 {
		t.Fatal(err)
	}
	if err = s.Configure(Config{Enabled: true, Format: "json", URL: "https://invalid.example"}, false); err == nil {
		t.Fatal("Agent accepted webhook configuration")
	}
}
func TestInvalidURLsStateAndRemoteFieldsAreRejected(t *testing.T) {
	s := fixture(t)
	for _, c := range []Config{{Enabled: true, Format: "json", URL: "file:///secret"}, {Enabled: true, Format: "json", URL: "https://user:secret@host/path"}, {Enabled: true, Format: "discord", URL: "https://attacker.invalid/api/webhooks/123/token"}, {Enabled: true, Format: "discord", URL: "http://discord.com/api/webhooks/123/token"}} {
		if err := s.Configure(c, false); err == nil {
			t.Fatal(c.Format)
		}
	}
	e := New("project", "apply", "completed", "", "")
	e.Action = "arbitrary shell"
	if err := s.Accept("host", "name", []Event{e}); err == nil {
		t.Fatal("Arbitrary action accepted")
	}
	os.Symlink(filepath.Join(filepath.Dir(s.file), "other"), s.file)
	if _, err := Open(filepath.Dir(filepath.Dir(s.file)), false); err == nil {
		t.Fatal("Symlinked state accepted")
	}
}

func TestEnablingWebhookDoesNotSendOldRemoteBacklog(t *testing.T) {
	s := fixture(t)
	before := New("project", "stop", "completed", "", "web")
	before.Time = time.Now().Add(-time.Hour)
	if err := s.Accept("host", "Ego", []Event{before}); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(Config{Enabled: true, Format: "json", URL: "https://receiver.invalid"}, false); err != nil {
		t.Fatal(err)
	}
	another := New("project", "start", "completed", "", "web")
	another.Time = time.Now().Add(-time.Minute)
	current := New("project", "save", "completed", "", "web")
	if err := s.Accept("host", "Ego", []Event{before, another, current}); err != nil {
		t.Fatal(err)
	}
	queued := s.Batch()
	if len(queued) != 1 || queued[0].ID != current.ID {
		t.Fatal("Events from before activation were disclosed", queued)
	}
}

func TestSlowWebhookDoesNotBlockNewActionEvents(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(204) }))
	defer receiver.Close()
	s := fixture(t)
	s.Configure(Config{Enabled: true, Format: "json", URL: receiver.URL}, false)
	s.Emit(New("project", "apply", "started", "", "web"))
	sent := make(chan struct{})
	go func() { s.SendOne(context.Background(), receiver.Client()); close(sent) }()
	defer func() { close(release); <-sent }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Delivery did not start")
	}
	queued := make(chan struct{})
	go func() { s.Emit(New("project", "apply", "completed", "", "web")); close(queued) }()
	select {
	case <-queued:
	case <-time.After(time.Second):
		t.Fatal("An action event blocked on webhook network I/O")
	}
	if s.View().Pending != 2 {
		t.Fatal(s.View())
	}
}
