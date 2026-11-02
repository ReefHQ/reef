package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"reef/internal/notifications"
)

func remoteManifestFingerprint(manifests []registryManifest, platforms []imagePlatform) (string, error) {
	if len(platforms) == 0 {
		return "", errors.New("No deployed platform to verify")
	}
	set := map[string]bool{}
	for _, p := range platforms {
		id, e := remoteImageID(manifests, p)
		if e != nil {
			return "", e
		}
		set[id] = true
	}
	keys := []string{}
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	raw, _ := json.Marshal(keys)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func (m *Manager) publishImageUpdateNotices(p Project, report ImageUpdates) {
	if report.Error != "" || report.Checking || report.Revision == "" {
		return
	}
	for _, row := range report.Services {
		if row.Status != "current" && row.Status != "available" {
			continue
		}
		details := notifications.UpdateDetails{ConfiguredTagChanged: row.Status == "available", ProjectID: p.ID, Project: notifications.Text(p.Name, 120), Service: row.Service, Image: row.Image}
		if row.VersionStatus == "available" && row.CandidateFingerprint != "" {
			details.Kind = "image_tag"
			details.Candidate = row.Candidate
			raw, _ := json.Marshal([]string{row.CandidateFingerprint, row.ConfiguredFingerprint})
			sum := sha256.Sum256(raw)
			details.Fingerprint = hex.EncodeToString(sum[:])
		} else if row.Status == "available" && row.ConfiguredFingerprint != "" {
			details.Kind = "image_digest"
			details.Fingerprint = row.ConfiguredFingerprint
		} else {
			continue
		}
		event := notifications.NewUpdate(details)
		if event.Valid() {
			m.emit(event)
		}
	}
}
func (s *Server) checkReefAndNotify(ctx context.Context) {
	if !s.reefUpdates.check(ctx) {
		return
	}
	status := s.reefUpdates.snapshot()
	if status.Status != "available" || !status.Available || status.Checking {
		return
	}
	event := notifications.ReefUpdate(status.Current, status.Latest)
	if event.Valid() {
		s.recordEvent(event)
	}
}
