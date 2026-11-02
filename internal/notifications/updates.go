package notifications

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

const MaxUpdateStreams = 4096
const MaxBatchBytes = 48 << 10
const MaxStateBytes = 4 << 20

var updateHash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var updateName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var updateVersion = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var updateImage = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,511}$`)

type UpdateDetails struct {
	ConfiguredTagChanged bool   `json:"configured_tag_changed,omitempty"`
	Kind                 string `json:"kind"`
	ProjectID            string `json:"project_id,omitempty"`
	Project              string `json:"project,omitempty"`
	Service              string `json:"service,omitempty"`
	Image                string `json:"image,omitempty"`
	Candidate            string `json:"candidate,omitempty"`
	Current              string `json:"current,omitempty"`
	Latest               string `json:"latest,omitempty"`
	Fingerprint          string `json:"fingerprint"`
}

func updateSum(parts ...string) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (d UpdateDetails) identity() (string, string) {
	return updateSum(d.Kind, d.ProjectID, d.Service), updateSum(d.Image, d.Candidate, d.Latest, d.Fingerprint)
}
func (d UpdateDetails) operation() string {
	stream, fp := d.identity()
	return updateSum(stream, fp)[:48]
}
func NewUpdate(d UpdateDetails) Event {
	action, target := "image_available", d.Project+" · "+d.Service
	if d.Kind == "reef" {
		action, target = "reef_available", "Reef"
	}
	e := New("update", action, "completed", d.operation(), target)
	e.Update = d
	return e
}
func (e Event) validUpdate() bool {
	if e.Scope != "update" {
		return e.Update == (UpdateDetails{})
	}
	d := e.Update
	if e.Status != "completed" || !updateHash.MatchString(d.Fingerprint) || e.Operation != d.operation() || e.Count != 0 {
		return false
	}
	switch d.Kind {
	case "reef":
		return !d.ConfiguredTagChanged && e.Action == "reef_available" && d.ProjectID == "" && d.Project == "" && d.Service == "" && d.Image == "" && d.Candidate == "" && len(d.Current) <= 128 && len(d.Latest) <= 128 && updateVersion.MatchString(d.Current) && updateVersion.MatchString(d.Latest) && d.Fingerprint == updateSum(d.Latest)
	case "image_digest", "image_tag":
		if e.Action != "image_available" || !updateName.MatchString(d.ProjectID) || len(d.Project) == 0 || len(d.Project) > 480 || Text(d.Project, 480) != d.Project || !updateName.MatchString(d.Service) || !updateImage.MatchString(d.Image) || strings.Contains(d.Image, "://") || d.Current != "" || d.Latest != "" {
			return false
		}
		if d.Kind == "image_digest" {
			return d.Candidate == ""
		}
		return d.Candidate != d.Image && updateImage.MatchString(d.Candidate) && !strings.Contains(d.Candidate, "://")
	}
	return false
}
func ReefUpdate(current, latest string) Event {
	return NewUpdate(UpdateDetails{Kind: "reef", Current: current, Latest: latest, Fingerprint: updateSum(latest)})
}
func updateKeys(e Event) (string, string) {
	stream, fp := e.Update.identity()
	return updateSum(e.HostID, stream), fp
}

// Remember an accepted notification, never a filtered observation. Queue and marker
// are persisted in the same transaction, so restarts do not repeat accepted alerts.
func rememberUpdate(next *disk, e Event) (bool, error) {
	if e.Scope != "update" {
		return true, nil
	}
	key, fp := updateKeys(e)
	if next.UpdateNotices[key] == fp {
		return false, nil
	}
	if len(next.UpdateNotices) >= MaxUpdateStreams && next.UpdateNotices[key] == "" {
		return false, errors.New("Update notification tracking limit reached; other notifications continue")
	}
	copy := make(map[string]string, len(next.UpdateNotices)+1)
	for k, v := range next.UpdateNotices {
		copy[k] = v
	}
	copy[key] = fp
	next.UpdateNotices = copy
	return true, nil
}
func (s *Service) emitUpdateLocked(e Event) {
	if s.relay {
		key, fp := updateKeys(e)
		for _, item := range s.data.Pending {
			if item.Event.Scope == "update" {
				oldKey, oldFP := updateKeys(item.Event)
				if key == oldKey && fp == oldFP {
					return
				}
			}
		}
	}
	old := s.data
	next := old
	if !s.relay {
		ok, err := rememberUpdate(&next, e)
		if err != nil {
			s.data.LastError = err.Error()
			s.data.Dropped++
			_ = s.saveLocked()
			return
		}
		if !ok {
			return
		}
	}
	if len(next.Pending) >= MaxPending {
		s.data.Dropped++
		_ = s.saveLocked()
		return
	}
	next.Pending = append(append([]pending{}, old.Pending...), pending{Event: e})
	s.data = next
	if s.saveLocked() != nil {
		s.data = old
	}
}

// Cancelled, expired or definitively failed deliveries can be retried after a
// new successful observation. Confirmed or still-pending notices stay deduplicated.
func forgetQueuedUpdates(next *disk, items []pending) {
	copied := false
	for _, item := range items {
		if item.Event.Scope != "update" {
			continue
		}
		key, fp := updateKeys(item.Event)
		if next.UpdateNotices[key] != fp {
			continue
		}
		if !copied {
			copy := make(map[string]string, len(next.UpdateNotices))
			for k, v := range next.UpdateNotices {
				copy[k] = v
			}
			next.UpdateNotices = copy
			copied = true
		}
		delete(next.UpdateNotices, key)
	}
}
