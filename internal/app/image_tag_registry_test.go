package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reef/internal/agents"
	"strings"
	"testing"
	"time"
)

func TestBrowserRegistryAndImplicitLatest(t *testing.T) {
	for _, tc := range []struct{ ref, provider, repo, tag string }{{"anthonyraymond/joal", "hub", "anthonyraymond/joal", "latest"}, {"ubuntu", "hub", "library/ubuntu", "latest"}, {"docker.io/library/ubuntu:24.04", "hub", "library/ubuntu", "24.04"}, {"lscr.io/linuxserver/deluge:latest", "lscr", "linuxserver/deluge", "latest"}, {"lscr.io/linuxserver/deluge", "lscr", "linuxserver/deluge", "latest"}, {"ghcr.io/team/app:v2", "v2:ghcr.io", "team/app", "v2"}, {"registry.example:5000/group/nested/app", "v2:registry.example:5000", "group/nested/app", "latest"}, {"quay.io/team/app:latest", "v2:quay.io", "team/app", "latest"}} {
		provider, repo, tag, ok := browserImageReference(tc.ref)
		if !ok || provider != tc.provider || repo != tc.repo || tag != tc.tag {
			t.Fatal(tc, provider, repo, tag, ok)
		}
	}
	for _, ref := range []string{"lscr.io/linuxserver/../bad:latest", "lscr.io/linuxserver/deluge@sha256:" + strings.Repeat("a", 64), "../bad"} {
		if _, _, _, ok := browserImageReference(ref); ok {
			t.Fatal(ref)
		}
	}
}
func TestLSCRAnonymousPagesAndBoundedSearch(t *testing.T) {
	calls, tagCalls := 0, 0
	client := &http.Client{Transport: reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		switch r.URL.Host {
		case "ghcr.io":
			if r.URL.Path != "/token" || r.URL.Query().Get("scope") != "repository:linuxserver/deluge:pull" || r.Header.Get("Authorization") != "" {
				t.Fatal(r)
			}
			return reefTagResponse(200, `{"token":"anonymous-test"}`), nil
		case "lscr.io":
			tagCalls++
			if r.URL.Path != "/v2/linuxserver/deluge/tags/list" || r.Header.Get("Authorization") != "Bearer anonymous-test" {
				t.Fatal(r)
			}
			start := 0
			if last := r.URL.Query().Get("last"); last != "" {
				fmt.Sscanf(last, "tag-%04d", &start)
				start++
			}
			limit := 50
			if r.URL.Query().Get("n") == "1000" {
				limit = 1000
			}
			tags := []string{}
			for i := start; i < start+limit; i++ {
				tags = append(tags, fmt.Sprintf("tag-%04d", i))
			}
			raw, _ := json.Marshal(map[string]any{"name": "linuxserver/deluge", "tags": tags})
			resp := reefTagResponse(200, string(raw))
			resp.Header.Set("Link", `<https://evil.invalid/do-not-follow>; rel="next"`)
			return resp, nil
		default:
			t.Fatalf("Unexpected token destination %s", r.URL.Host)
			return nil, nil
		}
	})}
	first, e := publicLSCRTagPage(context.Background(), "linuxserver/deluge", "", "", 1, client)
	if e != nil || len(first.Tags) != 50 || !first.More || first.NextCursor != "tag-0049" || first.Total != -1 {
		t.Fatal(first, e)
	}
	second, e := publicLSCRTagPage(context.Background(), "linuxserver/deluge", "", first.NextCursor, 2, client)
	if e != nil || second.Tags[0] != "tag-0050" {
		t.Fatal(second, e)
	}
	tagCalls = 0
	result, e := publicLSCRTagPage(context.Background(), "linuxserver/deluge", "latest", "", 1, client)
	if e != nil || len(result.Tags) != 0 || !result.More || tagCalls != 4 || result.NextCursor != "tag-3999" {
		t.Fatal(result, e, tagCalls)
	}
	before := calls
	if _, e := publicLSCRTagPage(context.Background(), "evil.invalid/deluge", "", "", 1, client); e == nil || calls != before {
		t.Fatal("Invalid registry contacted")
	}
}
func TestLSCRRefusesBadMetadataRedirectsAndCredentials(t *testing.T) {
	for _, raw := range []string{`{"name":"linuxserver/other","tags":["latest"]}`, `{"name":"linuxserver/deluge","tags":["same","same"]}`, `{"name":"linuxserver/deluge","tags":["../bad"]}`, `null`, strings.Repeat("x", (2<<20)+1)} {
		client := &http.Client{Transport: reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "ghcr.io" {
				return reefTagResponse(200, `{"token":"anonymous"}`), nil
			}
			return reefTagResponse(200, raw), nil
		})}
		if _, e := publicLSCRTagPage(context.Background(), "linuxserver/deluge", "", "", 1, client); e == nil {
			t.Fatal("Invalid page accepted")
		}
	}
	calls := 0
	client := &http.Client{Transport: reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := reefTagResponse(302, "")
		resp.Header.Set("Location", "https://evil.invalid")
		return resp, nil
	})}
	if _, e := publicLSCRTagPage(context.Background(), "linuxserver/deluge", "", "", 1, client); e == nil || calls != 1 {
		t.Fatal(e, calls)
	}
}
func TestRegistryAndImplicitTagsPrepareUnsavedImageOnlyDraft(t *testing.T) {
	for _, old := range []string{"anthonyraymond/joal", "lscr.io/linuxserver/deluge:latest", "ghcr.io/group/nested/app:latest"} {
		t.Run(old, func(t *testing.T) {
			s, p, x, before := tagServer(t)
			base, _ := imageTagParts(old)
			next := base + ":chosen-tag"
			x.base.oldRef = old
			x.base.newRef = next
			before = strings.Replace(before, gitlabOld, old, 1)
			os.WriteFile(filepath.Join(p.Directory, p.ComposeFiles[0]), []byte(before), 0600)
			s.m.Images.V2TagPage = func(context.Context, string, string, string, string, int) (imageTagPage, error) {
				return imageTagPage{Tags: []string{"latest", "chosen-tag"}, Page: 1, Total: -1}, nil
			}
			s.m.Images.RegistryTagPage = func(context.Context, string, string, string, int) (imageTagPage, error) {
				return imageTagPage{Tags: []string{"latest", "chosen-tag"}, Page: 1, Total: -1}, nil
			}
			rev, _ := revision(p)
			s.sessions[sessionKey("fixture")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
			cookie := &http.Cookie{Name: "qcu_session", Value: "fixture"}
			q := url.Values{"id": {p.ID}, "service": {"web"}, "revision": {rev}}
			w := call(s.Handler(), "GET", "/api/image-tags?"+q.Encode(), "", "", cookie)
			if w.Code != 200 || !strings.Contains(w.Body.String(), old) {
				t.Fatal(w.Code, w.Body)
			}
			form := url.Values{"id": {p.ID}, "service": {"web"}, "revision": {rev}, "candidate": {next}, "confirmed": {"yes"}, "csrf": {"csrf"}, "extended": {"yes"}}
			w = call(s.Handler(), "POST", "/api/image-tags/prepare", form.Encode(), "http://reef.test", cookie)
			if w.Code != 200 || !strings.Contains(w.Body.String(), next) {
				t.Fatal(w.Code, w.Body)
			}
			raw, _ := os.ReadFile(filepath.Join(p.Directory, p.ComposeFiles[0]))
			if string(raw) != before {
				t.Fatal("Saved source changed")
			}
			form.Set("candidate", "ghcr.io/linuxserver/deluge:chosen-tag")
			if w = call(s.Handler(), "POST", "/api/image-tags/prepare", form.Encode(), "http://reef.test", cookie); w.Code != 409 {
				t.Fatal(w.Code)
			}
		})
	}
}
func TestOlderTagAgentRejectsExtendedRequestsBeforeForwarding(t *testing.T) {
	central, _, cookie, _ := userServer(t)
	backend, _, _, _ := tagServer(t)
	host, stop := relayAgentFixture(t, central, backend, "old-tags", agents.ImageTagsCapability)
	defer stop()
	w := call(central.Handler(), "GET", "/api/image-tags?host="+host+"&extended=yes", "", "", cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "0.27.0") {
		t.Fatal(w.Code, w.Body)
	}
}

func TestLSCRRetainsRegistryOrderAndAllowsAnEmptyFinalPage(t *testing.T) {
	for _, tc := range []struct {
		raw, cursor string
		count       int
	}{{`{"name":"linuxserver/deluge","tags":["amd64-old","arm64-old","2.0-old","latest"]}`, "", 4}, {`{"name":"linuxserver/deluge","tags":null}`, "last-tag", 0}} {
		client := &http.Client{Transport: reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "ghcr.io" {
				return reefTagResponse(200, `{"token":"anon"}`), nil
			}
			return reefTagResponse(200, tc.raw), nil
		})}
		result, e := publicLSCRTagPage(context.Background(), "linuxserver/deluge", "", tc.cursor, 1, client)
		if e != nil || len(result.Tags) != tc.count || result.More {
			t.Fatal(result, e)
		}
	}
}

func TestLSCRTagBrowserUsesTheSelectedUpgradedAgent(t *testing.T) {
	central, _, cookie, _ := userServer(t)
	backend, p, x, before := tagServer(t)
	old := "lscr.io/linuxserver/deluge:latest"
	x.base.oldRef = old
	os.WriteFile(filepath.Join(p.Directory, p.ComposeFiles[0]), []byte(strings.Replace(before, gitlabOld, old, 1)), 0600)
	called := false
	backend.m.Images.RegistryTagPage = func(ctx context.Context, repo, search, cursor string, page int) (imageTagPage, error) {
		called = repo == "linuxserver/deluge" && cursor == "previous-tag"
		return imageTagPage{Tags: []string{"selected-tag"}, Total: -1, Page: 2, NextCursor: "selected-tag"}, nil
	}
	host, stop := relayAgentFixture(t, central, backend, "lscr-agent", agents.ImageTagsCapability, agents.ImageTagRegistriesCapability)
	defer stop()
	rev, _ := revision(p)
	q := url.Values{"host": {host}, "id": {p.ID}, "service": {"web"}, "revision": {rev}, "extended": {"yes"}, "cursor": {"previous-tag"}, "page": {"2"}}
	w := call(central.Handler(), "GET", "/api/image-tags?"+q.Encode(), "", "", cookie)
	if w.Code != 200 || !called || !strings.Contains(w.Body.String(), old) {
		t.Fatal(w.Code, called, w.Body)
	}
}

func TestGenericRegistryErrorCrossesOnlyTheSelectedAgentRelay(t *testing.T) {
	central, _, cookie, _ := userServer(t)
	backend, p, x, before := tagServer(t)
	old := "ghcr.io/team/app:latest"
	x.base.oldRef = old
	os.WriteFile(filepath.Join(p.Directory, p.ComposeFiles[0]), []byte(strings.Replace(before, gitlabOld, old, 1)), 0600)
	called := false
	backend.m.Images.V2TagPage = func(_ context.Context, host, repo, search, cursor string, page int) (imageTagPage, error) {
		called = host == "ghcr.io" && repo == "team/app"
		return imageTagPage{}, tagError("rate_limited", "Registry request limit reached; try again later")
	}
	host, stop := relayAgentFixture(t, central, backend, "generic-agent", agents.ImageTagsCapability, agents.ImageTagRegistriesCapability)
	defer stop()
	rev, _ := revision(p)
	q := url.Values{"host": {host}, "id": {p.ID}, "service": {"web"}, "revision": {rev}, "extended": {"yes"}}
	w := call(central.Handler(), "GET", "/api/image-tags?"+q.Encode(), "", "", cookie)
	if w.Code != 409 || !called || w.Header().Get("X-Reef-Registry-Error") != "rate_limited" {
		t.Fatal(w.Code, called, w.Header(), w.Body)
	}
}
