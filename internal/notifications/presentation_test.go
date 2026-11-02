package notifications

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func fieldValue(card eventPresentation, name string) string {
	for _, field := range card.Fields {
		if field.Name == name {
			return field.Value
		}
	}
	return ""
}
func TestDiscordCardsDistinguishOutcomesAndIncidents(t *testing.T) {
	for _, tc := range []struct {
		scope, action, status, label string
		color                        int
	}{{"project", "apply", "started", "In progress", 0x087384}, {"project", "apply", "completed", "Completed", 0x2f9e78}, {"project", "apply", "failed", "Failed", 0xd65a5a}, {"image", "cleanup", "partial", "Partially completed", 0xe5a54b}, {"container", "restart", "unknown", "Outcome unknown", 0xe5a54b}, {"console", "session", "closed", "Closed", 0x8796a6}, {"monitor", "unhealthy", "failed", "Incident opened", 0xd65a5a}, {"monitor", "unhealthy", "completed", "Recovered", 0x2f9e78}} {
		e := New(tc.scope, tc.action, tc.status, strings.Repeat("a", 48), "app")
		e.Host = "Docker host"
		card := presentEvent(e)
		if fieldValue(card, "Status") != tc.label || card.Color != tc.color || !strings.Contains(card.Footer.Text, e.Operation) || card.Timestamp != e.Time.UTC().Format(time.RFC3339Nano) || fieldValue(card, "Target") != "`app`" || fieldValue(card, "Host") != "`Docker host`" {
			t.Fatal(tc, card)
		}
	}
	for scope, list := range actions {
		for _, action := range strings.Fields(list) {
			if presentationTitles[scope+"."+action] == "" || presentationScopes[scope] == "" {
				t.Fatal("Missing friendly label", scope, action)
			}
		}
	}
}
func TestDiscordBatchCardsDoNotClaimZeroResultsWhileRunning(t *testing.T) {
	e := New("image", "cleanup", "started", "", "Unused images")
	e.Count = 10
	card := presentEvent(e)
	if fieldValue(card, "Items") != "10" || fieldValue(card, "Results") != "" {
		t.Fatal(card)
	}
	e.Status = "partial"
	e.Succeeded = 7
	e.Failed = 2
	e.Skipped = 1
	card = presentEvent(e)
	if fieldValue(card, "Results") != "7 succeeded · 2 failed · 1 skipped" {
		t.Fatal(card)
	}
	e.Count = 0
	card = presentEvent(e)
	if fieldValue(card, "Items") != "" || fieldValue(card, "Results") != "" {
		t.Fatal("Empty batch fields", card)
	}
}
func TestDiscordCardBoundsAndLiteralUntrustedMetadata(t *testing.T) {
	e := New("project", "apply", "failed", strings.Repeat("a", 48), "")
	e.Host = strings.Repeat("😀", 200)
	e.Target = strings.Repeat("@`", 320)
	if !e.Valid() {
		t.Fatal("Invalid limit fixture")
	}
	card := presentEvent(e)
	units := func(s string) int { return len(utf16.Encode([]rune(s))) }
	total := units(card.Title) + units(card.Description) + units(card.Author.Name) + units(card.Footer.Text)
	for _, f := range card.Fields {
		if units(f.Value) > 1024 || units(f.Name) > 256 {
			t.Fatal(f)
		}
		total += units(f.Value) + units(f.Name)
	}
	if total > 6000 || len(card.Fields) > 25 || units(card.Title) > 256 || units(card.Footer.Text) > 2048 {
		t.Fatal("Discord embed limits", total)
	}
	value := presentationValue("@everyone <@123> [link](https://example.invalid) ` ```\n*name*", 300)
	if strings.Count(value, "`") != 2 || strings.Contains(value, "@everyone") || strings.ContainsAny(value, "\r\n") {
		t.Fatal(value)
	}
	e.Target = ""
	if fieldValue(presentEvent(e), "Target") != "" {
		t.Fatal("Empty target field")
	}
}
func TestGenericWebhookAddsPresentationAndPreservesEventAndDeliveryOptions(t *testing.T) {
	e := New("container", "restart", "completed", strings.Repeat("b", 48), "app")
	raw, target, err := payload(Config{Format: "json", URL: "https://receiver.invalid/test?key=fixture"}, e)
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Schema       string
		Event        Event
		Presentation eventPresentation
	}
	if json.Unmarshal(raw, &data) != nil || data.Schema != "reef.action.v1" || data.Event != e || data.Presentation.Title != "Container restart" || strings.Contains(string(raw), "embeds") || strings.Contains(target, "wait=") {
		t.Fatal(string(raw), target)
	}
	raw, target, err = payload(Config{Format: "discord", URL: "https://discord.com/api/webhooks/123/fixture?thread_id=456"}, e)
	if err != nil || !strings.Contains(target, "wait=true") || !strings.Contains(target, "thread_id=456") {
		t.Fatal(err, target)
	}
	var wire map[string]json.RawMessage
	json.Unmarshal(raw, &wire)
	if wire["flags"] != nil || wire["content"] != nil || wire["embeds"] == nil || wire["allowed_mentions"] == nil {
		t.Fatal(string(raw))
	}
}

// Optional, synthetic-only export for the ignored local design preview. No network.
func TestExportWebhookDesignSamples(t *testing.T) {
	path := os.Getenv("REEF_WEBHOOK_PREVIEW_FILE")
	if path == "" {
		t.Skip("Local design preview export not requested")
	}
	events := []Event{}
	for _, tc := range []struct{ scope, action, status, target string }{{"project", "apply", "started", "media-stack"}, {"project", "apply", "completed", "media-stack"}, {"container", "restart", "failed", "worker"}, {"image", "cleanup", "partial", "Unused images"}, {"monitor", "unhealthy", "failed", "api"}, {"monitor", "unhealthy", "completed", "api"}, {"container", "restart", "unknown", "worker"}} {
		e := New(tc.scope, tc.action, tc.status, strings.Repeat("a", 48), tc.target)
		e.Host = "Docker host"
		e.Time = time.Date(2026, 11, 2, 14, 30, 0, 0, time.UTC)
		if tc.status == "partial" {
			e.Count = 10
			e.Succeeded = 7
			e.Failed = 2
			e.Skipped = 1
		}
		events = append(events, e)
	}
	digest := imageNotice("a")
	tag := imageNotice("b")
	tag.Update.Kind = "image_tag"
	tag.Update.Image = "example/app:1.2.0"
	tag.Update.Candidate = "example/app:1.2.1"
	tag = NewUpdate(tag.Update)
	events = append(events, digest, tag, ReefUpdate("0.31.0", "0.32.0"))
	for i := range events {
		events[i].Time = time.Date(2026, 11, 2, 14, 30, 0, 0, time.UTC)
	}
	cards := []eventPresentation{}
	for _, e := range events {
		cards = append(cards, presentEvent(e))
	}
	raw, err := json.MarshalIndent(cards, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGenericAndDiscordSharePresentationAcrossAllEvents(t *testing.T) {
	for scope, list := range actions {
		for _, action := range strings.Fields(list) {
			for _, status := range []string{"started", "completed", "failed", "partial", "unknown", "closed"} {
				e := New(scope, action, status, strings.Repeat("c", 48), "app @everyone `name`")
				e.Host = "Host @here"
				e.Count = 4
				e.Succeeded = 2
				e.Failed = 1
				e.Skipped = 1
				generic, _, err := payload(Config{Format: "json", URL: "https://receiver.invalid/test"}, e)
				if err != nil {
					t.Fatal(err)
				}
				discord, _, err := payload(Config{Format: "discord", URL: "https://discord.com/api/webhooks/123/fixture"}, e)
				if err != nil {
					t.Fatal(err)
				}
				var raw struct {
					Schema       string
					Event        Event
					Presentation eventPresentation
				}
				var wire struct{ Embeds []eventPresentation }
				if json.Unmarshal(generic, &raw) != nil || json.Unmarshal(discord, &wire) != nil || len(wire.Embeds) != 1 || !reflect.DeepEqual(raw.Presentation, wire.Embeds[0]) || raw.Schema != "reef.action.v1" || raw.Event != e {
					t.Fatal(scope, action, status, "Presentation drift or changed event")
				}
				// Legacy consumers still see exactly their known fields and original metadata.
				var legacy struct {
					Schema string
					Event  Event
				}
				if json.Unmarshal(generic, &legacy) != nil || legacy.Event != e || legacy.Schema != "reef.action.v1" {
					t.Fatal("Legacy receiver compatibility")
				}
			}
		}
	}
}
