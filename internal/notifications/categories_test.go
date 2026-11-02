package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCategorySelectionPersistsAndLegacyStateDefaultsToAll(t *testing.T) {
	s := fixture(t)
	config := Config{Enabled: true, Format: "json", URL: "https://receiver.invalid/fixture"}
	if err := s.Configure(config, false); err != nil {
		t.Fatal(err)
	}
	if len(s.View().Categories) != len(actions) || len(s.View().CategoryOptions) != len(actions) {
		t.Fatal(s.View())
	}
	for _, option := range CategoryOptions() {
		if _, ok := actions[option.ID]; !ok || option.Name == "" {
			t.Fatal(option)
		}
	}
	// A fixture written before categories existed still opts into all categories.
	raw, _ := os.ReadFile(s.file)
	var legacy map[string]json.RawMessage
	json.Unmarshal(raw, &legacy)
	var cfg map[string]json.RawMessage
	json.Unmarshal(legacy["config"], &cfg)
	delete(cfg, "categories")
	legacy["config"], _ = json.Marshal(cfg)
	raw, _ = json.Marshal(legacy)
	os.WriteFile(s.file, raw, 0600)
	reopened, err := Open(filepath.Dir(filepath.Dir(s.file)), false)
	if err != nil || len(reopened.View().Categories) != len(actions) {
		t.Fatal(err)
	}
	for _, selection := range [][]string{{"image", "project"}, {}} {
		config.Categories = selection
		if err = s.Configure(config, false); err != nil {
			t.Fatal(err)
		}
		reopened, err = Open(filepath.Dir(filepath.Dir(s.file)), false)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{}
		if len(selection) > 0 {
			want = []string{"project", "image"}
		}
		if !reflect.DeepEqual(reopened.View().Categories, want) {
			t.Fatal(reopened.View().Categories, want)
		}
	}
	for _, selection := range [][]string{{"invalid"}, {"project", "project"}} {
		config.Categories = selection
		if err = s.Configure(config, false); err == nil {
			t.Fatal("Invalid category selection accepted")
		}
	}
	config.Categories = []string{"project"}
	s.Configure(config, false)
	config.Categories[0] = "image"
	view := s.View()
	view.Categories[0] = "image"
	if s.View().Categories[0] != "project" {
		t.Fatal("Caller mutated saved preferences")
	}
}
func TestCategoriesFilterLocalAndRemoteWithoutDroppingOutboxOrTestEvents(t *testing.T) {
	s := fixture(t)
	s.Configure(Config{Enabled: true, Format: "json", URL: "https://receiver.invalid", Categories: []string{"project"}}, false)
	s.Emit(New("image", "pull", "completed", "", "image"))
	s.Emit(New("project", "start", "completed", "", "project"))
	if s.View().Pending != 1 || s.View().Dropped != 0 {
		t.Fatal(s.View())
	}
	remote := New("image", "pull", "completed", "", "remote image")
	if err := s.Accept("remote", "Remote", []Event{remote, remote}); err != nil || s.View().Pending != 1 {
		t.Fatal(err, s.View())
	}
	if len(s.data.Seen) != 1 {
		t.Fatal("Filtered remote event not durably acknowledged/deduplicated")
	}
	relay, err := Open(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	relay.Emit(remote)
	if len(relay.Batch()) != 1 {
		t.Fatal("Agent filtered central history metadata")
	}
	if err = relay.Ack([]string{remote.ID}); err != nil || len(relay.Batch()) != 0 {
		t.Fatal(err)
	}
	s.Configure(Config{Enabled: true, Format: "json", URL: "https://receiver.invalid", Categories: []string{}}, false)
	s.Emit(New("project", "restart", "completed", "", "project"))
	s.Emit(New("webhook", "configure", "completed", "", "settings"))
	s.Emit(New("webhook", "test", "completed", "", "test"))
	if s.View().Pending != 1 || s.Batch()[0].Action != "test" {
		t.Fatal(s.Batch())
	}
	// A replay of an excluded event must not appear when its category is enabled later.
	s.Configure(Config{Enabled: true, Format: "json", URL: "https://receiver.invalid", Categories: []string{"image"}}, false)
	if err = s.Accept("remote", "Remote", []Event{remote}); err != nil || s.View().Pending != 0 {
		t.Fatal(err, s.View())
	}
}
func TestExcludedCategoriesDoNotConsumeQueueCapacityOrCountAsFailures(t *testing.T) {
	s := fixture(t)
	s.Configure(Config{Enabled: true, Format: "json", URL: "https://receiver.invalid", Categories: []string{}}, false)
	for i := 0; i < MaxPending+1; i++ {
		s.Emit(New("image", "pull", "completed", "", "image"))
	}
	if s.View().Pending != 0 || s.View().Dropped != 0 {
		t.Fatal(s.View())
	}
	for i := 0; i < MaxPending; i++ {
		s.data.Pending = append(s.data.Pending, pending{Event: New("webhook", "test", "completed", "", "fixture")})
	}
	if err := s.Accept("remote", "Remote", []Event{New("image", "pull", "completed", "", "filtered")}); err != nil {
		t.Fatal("Filtered event rejected by a full queue", err)
	}
	// Defensive delivery policy for an excluded event already present in persisted state.
	s.data.Pending = []pending{{Event: New("image", "pull", "completed", "", "filtered"), Next: time.Now()}}
	if !s.SendOne(context.Background(), &http.Client{}) || s.View().Pending != 0 || s.View().Dropped != 0 {
		t.Fatal(s.View())
	}
}
