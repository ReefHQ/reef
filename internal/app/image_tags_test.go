package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reef/internal/agents"
	"strings"
	"testing"
	"time"
)

func TestPublicHubManualTagPages(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "hub.docker.com" || r.URL.Scheme != "https" || r.URL.Path != "/v2/namespaces/library/repositories/mariadb/tags" || r.Header.Get("Authorization") != "" || r.URL.Query().Get("page_size") != "50" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("name") != "11.4" {
			t.Fatal(r)
		}
		return reefTagResponse(200, `{"count":10000,"next":"https://evil.invalid/secret","results":[{"name":"11.4"},{"name":"11.4-noble"}]}`), nil
	})}
	page, err := publicHubTagPage(context.Background(), "library/mariadb", "11.4", 2, client)
	if err != nil || !page.More || page.Total != 10000 || len(page.Tags) != 2 || calls != 1 {
		t.Fatal(page, err, calls)
	}
	for _, raw := range []string{`null`, `{}`, `{"count":1,"results":[{"name":"../secret"}]}`, `{"count":2,"results":[{"name":"same"},{"name":"same"}]}`, `{"count":0,"results":[],"next":"https://evil.invalid"}`, strings.Repeat("x", (2<<20)+1)} {
		client.Transport = reefUpdateTransport(func(*http.Request) (*http.Response, error) { return reefTagResponse(200, raw), nil })
		if _, err := publicHubTagPage(context.Background(), "library/mariadb", "", 1, client); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
	calls = 0
	client.Transport = reefUpdateTransport(func(*http.Request) (*http.Response, error) {
		calls++
		r := reefTagResponse(302, "")
		r.Header.Set("Location", "https://evil.invalid")
		return r, nil
	})
	if _, err := publicHubTagPage(context.Background(), "library/mariadb", "", 1, client); err == nil || calls != 1 {
		t.Fatal(err, calls)
	}
	for _, tc := range []struct {
		repo, query string
		page        int
	}{{"evil.invalid/nested/app", "", 1}, {"library/mariadb", "x?secret", 1}, {"library/mariadb", "", 0}, {"library/mariadb", "", 1000001}} {
		if _, err := publicHubTagPage(context.Background(), tc.repo, tc.query, tc.page, client); err == nil {
			t.Fatal(tc)
		}
	}
}

type tagProposalExecutor struct {
	base                   *imageProposalExecutor
	mismatch, noContainers bool
	calls                  []string
}

func (x *tagProposalExecutor) Run(ctx context.Context, p Project, args []string, w io.Writer) error {
	cmd := strings.Join(args, " ")
	x.calls = append(x.calls, cmd)
	id := strings.Repeat("a", 64)
	imageID := "sha256:" + strings.Repeat("b", 64)
	switch {
	case strings.HasSuffix(cmd, "ps --all --quiet"):
		if !x.noContainers {
			io.WriteString(w, id)
		}
		return nil
	case len(args) > 1 && args[0] == "container":
		return json.NewEncoder(w).Encode(containerImage{ID: id, ImageID: imageID, Service: "web", Project: p.ProjectName})
	case len(args) > 1 && args[0] == "image":
		return json.NewEncoder(w).Encode(localImage{ID: imageID, imagePlatform: imagePlatform{OS: "linux", Architecture: "amd64"}})
	case len(args) > 1 && args[0] == "manifest":
		arch := "amd64"
		if x.mismatch {
			arch = "arm64"
		}
		return json.NewEncoder(w).Encode([]any{map[string]any{"Descriptor": map[string]any{"platform": map[string]string{"os": "linux", "architecture": arch}}, "OCIManifest": map[string]any{"config": map[string]string{"digest": imageID}}}})
	default:
		return x.base.Run(ctx, p, args, w)
	}
}
func tagServer(t *testing.T) (*Server, Project, *tagProposalExecutor, string) {
	m, p, x, content := imageProposalFixture(t)
	exec := &tagProposalExecutor{base: x}
	m.Images.Exec = exec
	m.Images.TagPage = func(context.Context, string, string, int) (imageTagPage, error) {
		return imageTagPage{Tags: []string{"latest", "19.5.3-ce.0"}, Total: 2000, Page: 1, More: true}, nil
	}
	s := newServer(m, Config{}, "long-password-123456")
	return s, p, exec, content
}
func TestSelectedImageTagPreparesOnlyUnsavedDraft(t *testing.T) {
	s, p, x, before := tagServer(t)
	rev, _ := revision(p)
	s.sessions[sessionKey("fixture")] = session{CSRF: "test-csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "fixture"}
	h := s.Handler()
	query := url.Values{"id": {p.ID}, "service": {"web"}, "revision": {rev}}
	get := call(h, "GET", "/api/image-tags?"+query.Encode(), "", "", cookie)
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"total":2000`) {
		t.Fatal(get.Code, get.Body)
	}
	form := url.Values{"id": {p.ID}, "service": {"web"}, "revision": {rev}, "candidate": {gitlabNext}, "confirmed": {"yes"}, "csrf": {"test-csrf"}}
	w := call(h, "POST", "/api/image-tags/prepare", form.Encode(), "http://reef.test", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), gitlabNext) {
		t.Fatal(w.Code, w.Body)
	}
	raw, _ := os.ReadFile(filepath.Join(p.Directory, p.ComposeFiles[0]))
	if string(raw) != before || s.m.Projects[p.ID].busy || s.m.Projects[p.ID].Op.ID != "" {
		t.Fatal("saved or started operation")
	}
	files, _ := filepath.Glob(filepath.Join(p.Directory, ".reef-image-candidate-*"))
	if len(files) > 0 {
		t.Fatal(files)
	}
	for _, cmd := range x.calls {
		if strings.Contains(cmd, " pull ") || strings.Contains(cmd, " up ") || strings.Contains(cmd, " down ") {
			t.Fatal(cmd)
		}
	}
	for _, candidate := range []string{"evil.invalid/app:latest", "other/app:1.0", gitlabOld, "gitlab/gitlab-ce@sha256:" + strings.Repeat("f", 64)} {
		form.Set("candidate", candidate)
		if got := call(h, "POST", "/api/image-tags/prepare", form.Encode(), "http://reef.test", cookie); got.Code != 409 {
			t.Fatal(candidate, got.Code)
		}
	}
	form.Set("candidate", gitlabNext)
	form.Set("confirmed", "no")
	if got := call(h, "POST", "/api/image-tags/prepare", form.Encode(), "http://reef.test", cookie); got.Code != 400 {
		t.Fatal(got.Code)
	}
	form.Set("confirmed", "yes")
	form.Set("csrf", "wrong")
	if got := call(h, "POST", "/api/image-tags/prepare", form.Encode(), "http://reef.test", cookie); got.Code != 403 {
		t.Fatal(got.Code)
	}
}
func TestTagPreparationRefusesUnsafeOrStaleProjects(t *testing.T) {
	for _, mode := range []string{"stale", "busy", "platform", "not deployed", "side effect", "override"} {
		t.Run(mode, func(t *testing.T) {
			s, p, x, _ := tagServer(t)
			rev, _ := revision(p)
			switch mode {
			case "stale":
				rev = "stale"
			case "busy":
				s.m.Projects[p.ID].busy = true
			case "platform":
				x.mismatch = true
			case "not deployed":
				x.noContainers = true
			case "side effect":
				x.base.sideEffect = true
			case "override":
				x.base.override = true
			}
			s.sessions[sessionKey("fixture")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
			form := url.Values{"id": {p.ID}, "service": {"web"}, "revision": {rev}, "candidate": {gitlabNext}, "confirmed": {"yes"}, "csrf": {"csrf"}}
			w := call(s.Handler(), "POST", "/api/image-tags/prepare", form.Encode(), "http://reef.test", &http.Cookie{Name: "qcu_session", Value: "fixture"})
			if w.Code != 409 {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
}
func TestTagBrowserPermissionsAndRemoteIsolation(t *testing.T) {
	central, _, admin, csrf := userServer(t)
	remote, p, _, _ := tagServer(t)
	host, stop := relayAgentFixture(t, central, remote, "tag-agent", agents.ImageTagsCapability)
	defer stop()
	rev, _ := revision(p)
	query := "?" + url.Values{"host": {host}, "id": {p.ID}, "service": {"web"}, "revision": {rev}}.Encode()
	viewer := addReader(t, central, "viewer", host)
	cookie, viewerCSRF := userCookie(t, central, viewer)
	if w := call(central.Handler(), "GET", "/api/image-tags"+query, "", "", cookie); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(central.Handler(), "GET", "/api/image-tags", "", "", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	form := url.Values{"id": {p.ID}, "service": {"web"}, "revision": {rev}, "candidate": {gitlabNext}, "confirmed": {"yes"}, "csrf": {viewerCSRF}}
	if w := call(central.Handler(), "POST", "/api/image-tags/prepare"+query, form.Encode(), "http://reef.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	form.Set("csrf", csrf)
	if w := call(central.Handler(), "POST", "/api/image-tags/prepare"+query, form.Encode(), "http://reef.test", admin); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	remote.m.Images.TagPage = func(context.Context, string, string, int) (imageTagPage, error) {
		return imageTagPage{}, errors.New("unavailable")
	}
	if w := call(central.Handler(), "GET", "/api/image-tags"+query, "", "", admin); w.Code != 409 {
		t.Fatal(w.Code)
	}
}
