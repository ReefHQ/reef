package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
)

func replaceLiteralImage(content, old, next string) (string, error) {
	pattern := regexp.MustCompile(`(?m)^([ \t]*image:[ \t]*)(?:` + regexp.QuoteMeta(old) + `|"` + regexp.QuoteMeta(old) + `"|'` + regexp.QuoteMeta(old) + `')([ \t]*(?:#[^\r\n]*)?)(\r?)$`)
	matches := pattern.FindAllStringSubmatchIndex(content, -1)
	if len(matches) != 1 {
		return "", errors.New("Automatic editing requires one literal image line in the main Compose file. Copy the proposed reference into the appropriate Compose or env file manually")
	}
	m := matches[0]
	return content[:m[3]] + next + content[m[4]:], nil
}
func (s *ImageService) validateImageProposal(ctx context.Context, p Project, service, old, next, content string) error {
	raw, e := s.command(ctx, p, composeArgs(p, "", "config", "--format", "json")...)
	if e != nil {
		return e
	}
	var before map[string]any
	if json.Unmarshal(raw, &before) != nil {
		return errors.New("Cannot inspect saved Compose configuration")
	}
	services, ok := before["services"].(map[string]any)
	if !ok {
		return errors.New("Cannot inspect saved Compose services")
	}
	target, ok := services[service].(map[string]any)
	if !ok || target["image"] != old {
		return errors.New("Saved service image changed; check images again")
	}
	candidate, e := os.CreateTemp(filepath.Dir(filepath.Join(p.Directory, p.ComposeFiles[0])), ".reef-image-candidate-*.yaml")
	if e != nil {
		return errors.New("Cannot prepare a private Compose candidate")
	}
	path := candidate.Name()
	defer os.Remove(path)
	_, e = candidate.WriteString(content)
	closeErr := candidate.Close()
	if e != nil || closeErr != nil {
		return errors.New("Cannot prepare Compose candidate")
	}
	raw, e = s.command(ctx, p, composeArgs(p, path, "config", "--format", "json")...)
	if e != nil {
		return errors.New("Proposed Compose configuration is invalid")
	}
	var after map[string]any
	if json.Unmarshal(raw, &after) != nil {
		return errors.New("Cannot inspect proposed Compose configuration")
	}
	target["image"] = next
	if !reflect.DeepEqual(before, after) {
		return errors.New("Image edit changes other Compose settings or is overridden. Edit the appropriate Compose or env file manually")
	}
	return nil
}
func (m *Manager) imageVersionProposal(ctx context.Context, id, service, rev, expectedCandidate string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.Projects[id]
	if !ok || m.Images == nil {
		return "", errors.New("Project image checks unavailable")
	}
	p.mu.Lock()
	if m.agentUpdating || p.busy || p.ImageUpdates.Checking || p.ImageUpdates.Revision != rev || rev == "" {
		p.mu.Unlock()
		return "", errors.New("Project or image report changed; refresh and check images again")
	}
	old, next := "", ""
	for _, row := range p.ImageUpdates.Services {
		if row.Service == service && row.VersionStatus == "available" && row.Candidate == expectedCandidate {
			old, next = row.Image, row.Candidate
			break
		}
	}
	if old == "" || next == "" || !validCatalogReference(next, true) {
		p.mu.Unlock()
		return "", errors.New("No verified patch tag proposal for this service")
	}
	p.busy = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.busy = false; p.mu.Unlock() }()
	if e := sameRevision(p.Project, rev); e != nil {
		return "", e
	}
	raw, e := os.ReadFile(filepath.Join(p.Project.Directory, p.Project.ComposeFiles[0]))
	if e != nil || len(raw) > 1<<20 {
		return "", errors.New("Cannot read main Compose file within editing limit")
	}
	content, e := replaceLiteralImage(string(raw), old, next)
	if e != nil {
		return "", e
	}
	if e = m.Images.validateImageProposal(ctx, p.Project, service, old, next, content); e != nil {
		return "", e
	}
	if e = sameRevision(p.Project, rev); e != nil {
		return "", e
	}
	return content, nil
}
func (s *Server) prepareImageVersion(w http.ResponseWriter, r *http.Request) {
	if r.Form.Get("confirmed") != "yes" {
		http.Error(w, "Explicit confirmation is required to prepare a tag change", 400)
		return
	}
	content, e := s.m.imageVersionProposal(r.Context(), r.Form.Get("id"), r.Form.Get("service"), r.Form.Get("revision"), r.Form.Get("candidate"))
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	// Return the same saved-file revision, not a claim that the proposal was saved.
	writeJSON(w, map[string]string{"content": content, "revision": r.Form.Get("revision")})
}
