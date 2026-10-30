package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestPatchVersionPolicyAndHubReferences(t *testing.T) {
	for _, tag := range []string{"19.5.1-ce.0", "v1.2.3", "1.2.3-alpine3.21"} {
		if _, ok := parsePatchVersion(tag); !ok {
			t.Fatal(tag)
		}
	}
	for _, tag := range []string{"latest", "1.2", "01.2.3", "1.2.3-rc1", "1.2.3-beta1", "1.2.4294967296"} {
		if _, ok := parsePatchVersion(tag); ok {
			t.Fatal(tag)
		}
	}
	for _, ref := range []string{"gitlab/gitlab-ce:19.5.1-ce.0", "docker.io/gitlab/gitlab-ce:19.5.1-ce.0", "index.docker.io/gitlab/gitlab-ce:19.5.1-ce.0", "nginx:1.2.3"} {
		if _, _, ok := hubVersionReference(ref); !ok {
			t.Fatal(ref)
		}
	}
	for _, ref := range []string{"evil.test/app:1.2.3", "localhost/app:1.2.3", "docker.io.evil.test/app:1.2.3", "nginx@" + oldImage} {
		if _, _, ok := hubVersionReference(ref); ok {
			t.Fatal(ref)
		}
	}
}
func TestImagePatchDiscoveryFindsNewTagWithoutPullOrFileChange(t *testing.T) {
	x := &imageExecutor{imageRef: "gitlab/gitlab-ce:19.5.1-ce.0", registryID: oldImage}
	_, p := adoptionFixture(t)
	calls := 0
	s := &ImageService{Exec: x, ListTags: func(ctx context.Context, repo, prefix string) ([]string, error) {
		calls++
		if repo != "gitlab/gitlab-ce" || prefix != "19.5." {
			t.Fatal(repo, prefix)
		}
		return []string{"19.5.1-ce.0", "19.5.3-ce.0", "19.5.10-ce.0", "19.6.1-ce.0", "20.5.1-ce.0", "19.5.11-ee.0", "19.5.99-ce.0-rc1"}, nil
	}}
	rows, e := s.check(context.Background(), p)
	if e != nil || len(rows) != 1 || rows[0].Status != "current" || rows[0].VersionStatus != "available" || rows[0].Candidate != "gitlab/gitlab-ce:19.5.10-ce.0" {
		t.Fatal(rows, e)
	}
	if _, e = s.check(context.Background(), p); e != nil || calls != 1 {
		t.Fatal("tag cache not used", calls, e)
	}
	for _, bad := range []string{" pull ", " up ", " down ", "image rm"} {
		if strings.Contains(x.commands(), bad) {
			t.Fatal("check mutated Docker")
		}
	}
}
func TestPatchTagSearchFailuresAreSeparateFromConfiguredTag(t *testing.T) {
	for _, mode := range []string{"current", "failed", "incomplete", "unsupported", "platform"} {
		t.Run(mode, func(t *testing.T) {
			x := &imageExecutor{registryID: oldImage}
			s := &ImageService{Exec: x}
			row := ImageUpdate{Image: "gitlab/gitlab-ce:19.5.1-ce.0", Status: "current"}
			s.ListTags = func(context.Context, string, string) ([]string, error) {
				switch mode {
				case "failed":
					return nil, errors.New("secret error")
				case "incomplete":
					return nil, nil
				case "platform":
					return []string{"19.5.3-ce.0"}, nil
				}
				return []string{"19.5.1-ce.0"}, nil
			}
			if mode == "unsupported" {
				row.Image = "private.test/app:1.2.3"
			}
			platform := imagePlatform{OS: "linux", Architecture: "amd64"}
			if mode == "platform" {
				platform.Architecture = "s390x"
			}
			s.checkPatchVersion(context.Background(), Project{}, &row, []imagePlatform{platform})
			want := "unknown"
			if mode == "current" {
				want = "current"
			}
			if mode == "unsupported" {
				want = "unsupported"
			}
			if row.VersionStatus != want || row.Status != "current" || row.Candidate != "" || strings.Contains(row.VersionMessage, "secret") {
				t.Fatal(row)
			}
		})
	}
}

type hubRoundTrip func(*http.Request) (*http.Response, error)

func (f hubRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestHubTagPaginationStaysOnFixedOriginAndFailsClosed(t *testing.T) {
	for _, mode := range []string{"pagination", "oversize", "partial", "redirect", "invalid", "rate limit"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: hubRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "hub.docker.com" || r.Header.Get("Authorization") != "" || r.URL.Query().Get("name") != "19.5." {
					t.Fatal("unsafe tag request")
				}
				status := 200
				body := `{"count":2,"next":"https://evil.test/secret","results":[{"name":"19.5.1-ce.0"}]}`
				switch mode {
				case "pagination":
					if calls == 2 {
						body = `{"count":2,"next":null,"results":[{"name":"19.5.3-ce.0"}]}`
					}
				case "oversize":
					body = `{"count":501,"next":null,"results":[]}`
				case "partial":
					body = `{"count":2,"next":null,"results":[]}`
				case "redirect":
					status = 302
				case "invalid":
					body = `{"count":1,"next":null,"results":[{"name":"bad tag"}]}`
				case "rate limit":
					status = 429
					body = "secret body"
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://evil.test"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			tags, e := publicHubPatchTags(context.Background(), "gitlab/gitlab-ce", "19.5.", client)
			if (e == nil) != (mode == "pagination") {
				t.Fatal(mode, tags, e)
			}
			if mode == "pagination" && (len(tags) != 2 || calls != 2) {
				t.Fatal(tags, calls)
			}
			if e != nil && strings.Contains(e.Error(), "secret") {
				t.Fatal(e)
			}
		})
	}
}
func TestLivePublicGitlabPatchTags(t *testing.T) {
	if os.Getenv("REEF_TEST_HUB_TAGS") != "1" {
		t.Skip("opt-in public registry metadata read")
	}
	tags, e := publicHubPatchTags(context.Background(), "gitlab/gitlab-ce", "19.5.", nil)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, tag := range tags {
		if tag == "19.5.3-ce.0" {
			found = true
		}
	}
	if !found {
		t.Fatal("reported GitLab patch tag not found")
	}
	t.Logf("Read %d public GitLab tags; patch 19.5.3-ce.0 present; no Docker action", len(tags))
}
