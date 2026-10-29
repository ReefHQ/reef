// Package notifications sends bounded action metadata, never application output.
package notifications

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const MaxPending = 512

var eventID = regexp.MustCompile(`^[0-9a-f]{48}$`)
var actions = map[string]string{
	"schedule": " configure remove run ",
	"monitor":  " disconnected unhealthy restart_loop ",
	"project":  " save apply start stop register unmanage ",
	"image":    " pull tag untag remove cleanup check ",
	"network":  " create remove cleanup connect disconnect ",
	"volume":   " create remove cleanup backup restore backup_delete ",
	"build":    " build cache_cleanup ",
	"agent":    " enroll rename revoke update ",
	"console":  " session ",
	"webhook":  " configure test ",
}

type Event struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	HostID    string    `json:"host_id"`
	Host      string    `json:"host"`
	Scope     string    `json:"scope"`
	Action    string    `json:"action"`
	Status    string    `json:"status"`
	Operation string    `json:"operation_id,omitempty"`
	Target    string    `json:"target,omitempty"`
	Count     int       `json:"count,omitempty"`
	Succeeded int       `json:"succeeded,omitempty"`
	Failed    int       `json:"failed,omitempty"`
	Skipped   int       `json:"skipped,omitempty"`
}

func New(scope, action, status, operation, target string) Event {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return Event{ID: hex.EncodeToString(b), Time: time.Now().UTC(), HostID: "local", Host: "Local", Scope: scope, Action: action, Status: status, Operation: operation, Target: Text(target, 160)}
}
func Text(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	r := []rune(s)
	if len(r) > limit {
		s = string(r[:limit])
	}
	return s
}
func (e Event) Valid() bool {
	if !eventID.MatchString(e.ID) || e.Time.IsZero() || e.Time.After(time.Now().Add(5*time.Minute)) || len(e.Target) > 640 || len(e.Host) > 800 || len(e.HostID) > 100 {
		return false
	}
	if e.Operation != "" && !eventID.MatchString(e.Operation) {
		return false
	}
	if !strings.Contains(actions[e.Scope], " "+e.Action+" ") || e.Action == "" {
		return false
	}
	switch e.Status {
	case "started", "completed", "failed", "partial", "closed", "unknown":
	default:
		return false
	}
	return e.Count >= 0 && e.Count <= 10000 && e.Succeeded >= 0 && e.Succeeded <= 10000 && e.Failed >= 0 && e.Failed <= 10000 && e.Skipped >= 0 && e.Skipped <= 10000
}
func (e Event) Key() string { return e.HostID + ":" + e.ID }
func (e Event) Message() string {
	s := fmt.Sprintf("Reef · %s · %s %s · %s", e.Host, e.Scope, e.Action, e.Status)
	if e.Scope == "monitor" {
		if e.Status == "failed" {
			s += " · incident opened"
		} else if e.Status == "completed" {
			s += " · recovery observed"
		}
	}
	if e.Target != "" {
		s += "\nTarget: " + e.Target
	}
	if e.Operation != "" {
		s += "\nOperation: " + e.Operation
	}
	if e.Count > 0 {
		s += fmt.Sprintf("\nItems: %d · completed: %d · failed: %d · skipped: %d", e.Count, e.Succeeded, e.Failed, e.Skipped)
	}
	s += "\n" + e.Time.Format(time.RFC3339)
	// Prevent Markdown/mention formatting of operator-controlled names.
	return strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "~", "\\~", ">", "\\>", "<", "\\<", "[", "\\[", "]", "\\]", "@", "@\u200b").Replace(s)
}
