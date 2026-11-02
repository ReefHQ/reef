package notifications

import (
	"fmt"
	"strings"
	"time"
)

type presentationField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}
type eventPresentation struct {
	Author struct {
		Name string `json:"name"`
	} `json:"author"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Color       int                 `json:"color"`
	Fields      []presentationField `json:"fields"`
	Footer      struct {
		Text string `json:"text"`
	} `json:"footer"`
	Timestamp string `json:"timestamp"`
}

var presentationScopes = map[string]string{"container": "Containers", "project": "Projects", "image": "Images", "volume": "Volumes", "network": "Networks", "build": "Builds", "agent": "Hosts", "console": "Consoles", "webhook": "Webhooks", "monitor": "Alerts", "schedule": "Schedules"}
var presentationTitles = map[string]string{
	"container.start": "Container start", "container.stop": "Container stop", "container.restart": "Container restart", "container.update": "Container image update", "container.remove": "Container deletion",
	"project.save": "Compose file save", "project.apply": "Project update", "project.start": "Project start", "project.stop": "Project stop", "project.restart": "Project restart", "project.register": "Project connected", "project.unmanage": "Project management removed",
	"image.pull": "Image pull", "image.tag": "Image tagging", "image.untag": "Image tag removal", "image.remove": "Image deletion", "image.cleanup": "Image cleanup", "image.check": "Image update check",
	"volume.create": "Volume creation", "volume.remove": "Volume deletion", "volume.cleanup": "Volume cleanup", "volume.backup": "Volume backup", "volume.restore": "Volume restore", "volume.backup_delete": "Volume backup deletion",
	"network.create": "Network creation", "network.remove": "Network deletion", "network.cleanup": "Network cleanup", "network.connect": "Network connection", "network.disconnect": "Network disconnection",
	"build.build": "Project build", "build.cache_cleanup": "Build cache cleanup",
	"agent.enroll": "Host enrolled", "agent.rename": "Host renamed", "agent.revoke": "Host access revoked", "agent.update": "Agent update",
	"console.session": "Console session", "webhook.configure": "Webhook settings", "webhook.test": "Test notification",
	"monitor.disconnected": "Host connection", "monitor.unhealthy": "Container health", "monitor.restart_loop": "Container restart loop",
	"schedule.configure": "Schedule settings", "schedule.remove": "Schedule removed", "schedule.run": "Scheduled operation",
}

// Metadata stays literal: no injected markdown, mentions, links or code delimiters.
// Backticks become a visually similar apostrophe within a single inline code span.
func presentationValue(value string, limit int) string {
	value = strings.TrimSpace(Text(value, limit+1))
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit-1]) + "…"
	}
	if value == "" {
		value = "Not specified"
	}
	return "`" + strings.NewReplacer("`", "ˋ", "@", "@\u200b").Replace(value) + "`"
}
func presentEvent(e Event) eventPresentation {
	card := eventPresentation{Title: presentationTitles[e.Scope+"."+e.Action], Timestamp: e.Time.UTC().Format(time.RFC3339Nano)}
	if card.Title == "" {
		card.Title = "Reef action"
	}
	scope := presentationScopes[e.Scope]
	if scope == "" {
		scope = "Actions"
	}
	card.Author.Name = "Reef · " + scope
	status, description, color := "Outcome unknown", "The outcome could not be confirmed. Check the host in Reef before retrying.", 0xe5a54b
	switch e.Status {
	case "started":
		status, description, color = "In progress", "The action is running on its host.", 0x087384
	case "completed":
		status, description, color = "Completed", "The action completed successfully.", 0x2f9e78
	case "failed":
		status, description, color = "Failed", "The action did not complete successfully. Review its details in Reef.", 0xd65a5a
	case "partial":
		status, description, color = "Partially completed", "Some items need attention. Review the results in Reef.", 0xe5a54b
	case "closed":
		status, description, color = "Closed", "The action has ended.", 0x8796a6
	}
	if e.Scope == "monitor" && e.Status == "failed" {
		status, description = "Incident opened", "A condition needs attention. Open Alerts in Reef for details."
	}
	if e.Scope == "monitor" && e.Status == "completed" {
		status, description = "Recovered", "The condition has cleared. Recovery was observed by Reef."
	}
	if e.Scope == "console" && e.Status == "closed" {
		description = "This console session has ended."
	}
	card.Description = description
	card.Color = color
	card.Fields = []presentationField{{Name: "Status", Value: status, Inline: true}, {Name: "Host", Value: presentationValue(e.Host, 200), Inline: true}}
	if e.Target != "" {
		card.Fields = append(card.Fields, presentationField{Name: "Target", Value: presentationValue(e.Target, 300)})
	}
	if e.Count > 0 {
		card.Fields = append(card.Fields, presentationField{Name: "Items", Value: fmt.Sprint(e.Count), Inline: true})
		if e.Status != "started" {
			card.Fields = append(card.Fields, presentationField{Name: "Results", Value: fmt.Sprintf("%d succeeded · %d failed · %d skipped", e.Succeeded, e.Failed, e.Skipped)})
		}
	}
	card.Footer.Text = "Action notifications"
	if e.Operation != "" {
		card.Footer.Text = "Operation · " + e.Operation
	}
	return card
}
