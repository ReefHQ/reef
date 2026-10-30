package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// New-tag discovery is deliberately public Docker Hub only. Never send Docker
// credentials, accept arbitrary registry URLs, or follow response redirects/links.
var patchVersionPattern = regexp.MustCompile(`^(v?)(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[A-Za-z0-9][A-Za-z0-9_.-]*)?$`)
var unstableVersionPattern = regexp.MustCompile(`(?i)(^|[._-])(alpha[0-9]*|beta[0-9]*|rc[0-9]*|dev|nightly|preview[0-9]*|snapshot)([._-]|$)`)

type patchVersion struct {
	Prefix, Major, Minor, Suffix string
	Patch                        uint64
}

func parsePatchVersion(tag string) (patchVersion, bool) {
	m := patchVersionPattern.FindStringSubmatch(tag)
	if m == nil || unstableVersionPattern.MatchString(m[5]) {
		return patchVersion{}, false
	}
	n, e := strconv.ParseUint(m[4], 10, 32)
	if e != nil {
		return patchVersion{}, false
	}
	return patchVersion{m[1], m[2], m[3], m[5], n}, true
}
func hubVersionReference(ref string) (repository, tag string, ok bool) {
	if !validCatalogReference(ref, true) {
		return "", "", false
	}
	at := strings.LastIndex(ref, ":")
	name, tag := ref[:at], ref[at+1:]
	parts := strings.Split(name, "/")
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		if parts[0] != "docker.io" && parts[0] != "index.docker.io" {
			return "", "", false
		}
		parts = parts[1:]
	}
	if len(parts) == 1 {
		parts = append([]string{"library"}, parts...)
	}
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.Join(parts, "/"), tag, true
}

type patchTagsCache struct {
	At   time.Time
	Tags []string
}

func publicHubPatchTags(ctx context.Context, repository, prefix string, client *http.Client) ([]string, error) {
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		defer transport.CloseIdleConnections()
		client = &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &safeClient
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !repositoryPattern.MatchString(repository) {
		return nil, errors.New("Unsupported Docker Hub repository")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tags := []string{}
	seen := map[string]bool{}
	for page := 1; page <= 5; page++ {
		endpoint := "https://hub.docker.com/v2/namespaces/" + url.PathEscape(parts[0]) + "/repositories/" + url.PathEscape(parts[1]) + "/tags?" + url.Values{"name": {prefix}, "page_size": {"100"}, "page": {strconv.Itoa(page)}}.Encode()
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if e != nil {
			return nil, errors.New("Cannot prepare tag lookup")
		}
		response, e := client.Do(req)
		if e != nil {
			return nil, errors.New("Docker Hub tag search unavailable")
		}
		raw, e := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		response.Body.Close()
		if e != nil || len(raw) > 2<<20 || response.StatusCode != 200 {
			return nil, errors.New("Docker Hub tag search unavailable, private or rate limited")
		}
		var data struct {
			Count   *int    `json:"count"`
			Next    *string `json:"next"`
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		}
		if json.Unmarshal(raw, &data) != nil || data.Count == nil || *data.Count < 0 || *data.Count > 500 || len(data.Results) > 100 {
			return nil, errors.New("Docker Hub tag search incomplete or too large")
		}
		for _, item := range data.Results {
			if !tagPattern.MatchString(item.Name) || seen[item.Name] {
				return nil, errors.New("Docker Hub returned invalid tag metadata")
			}
			seen[item.Name] = true
			tags = append(tags, item.Name)
		}
		// The server's next link is only a pagination signal, never a request URL.
		if data.Next == nil {
			if len(tags) != *data.Count {
				return nil, errors.New("Docker Hub tag search incomplete")
			}
			return tags, nil
		}
	}
	return nil, errors.New("Docker Hub tag search incomplete; pagination limit reached")
}
func (s *ImageService) patchTags(ctx context.Context, repo, prefix string) ([]string, error) {
	key := repo + ":" + prefix
	s.mu.Lock()
	defer s.mu.Unlock()
	if hit, ok := s.tagCache[key]; ok && time.Since(hit.At) < 10*time.Minute {
		return append([]string{}, hit.Tags...), nil
	}
	lookup := s.ListTags
	if lookup == nil {
		lookup = func(ctx context.Context, repo, prefix string) ([]string, error) {
			return publicHubPatchTags(ctx, repo, prefix, nil)
		}
	}
	tags, e := lookup(ctx, repo, prefix)
	if e != nil {
		return nil, e
	}
	if len(tags) > 500 {
		return nil, errors.New("Tag search is too large")
	}
	if s.tagCache == nil || len(s.tagCache) >= 128 {
		s.tagCache = map[string]patchTagsCache{}
	}
	s.tagCache[key] = patchTagsCache{time.Now(), append([]string{}, tags...)}
	return tags, nil
}
func (s *ImageService) checkPatchVersion(ctx context.Context, p Project, row *ImageUpdate, platforms []imagePlatform) {
	repo, tag, ok := hubVersionReference(row.Image)
	current, versioned := parsePatchVersion(tag)
	if !ok || !versioned {
		row.VersionStatus = "unsupported"
		row.VersionMessage = "New-tag search supports public Docker Hub images with a full version tag; only the configured tag was compared"
		return
	}
	row.VersionStatus = "unknown"
	prefix := current.Prefix + current.Major + "." + current.Minor + "."
	tags, e := s.patchTags(ctx, repo, prefix)
	if e != nil {
		row.VersionMessage = "New-tag search unavailable or incomplete; configured-tag comparison is separate"
		return
	}
	candidates := []string{}
	foundCurrent := false
	for _, tag := range tags {
		if tag == tagFromImage(row.Image) {
			foundCurrent = true
		}
		v, ok := parsePatchVersion(tag)
		if ok && v.Prefix == current.Prefix && v.Major == current.Major && v.Minor == current.Minor && v.Suffix == current.Suffix && v.Patch > current.Patch {
			candidates = append(candidates, tag)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, _ := parsePatchVersion(candidates[i])
		b, _ := parsePatchVersion(candidates[j])
		return a.Patch > b.Patch
	})
	if len(candidates) == 0 {
		if !foundCurrent {
			row.VersionMessage = "Current tag is not listed; new-tag search is inconclusive"
			return
		}
		row.VersionStatus = "current"
		row.VersionMessage = "No newer patch tag found in the same major/minor series and variant"
		return
	}
	candidate := row.Image[:strings.LastIndex(row.Image, ":")+1] + candidates[0]
	manifests, e := s.manifests(ctx, p, candidate)
	if e != nil {
		row.VersionMessage = "Newer patch tag found, but its registry manifest could not be verified"
		return
	}
	for _, platform := range platforms {
		if _, e = remoteImageID(manifests, platform); e != nil {
			row.VersionMessage = "Newer patch tag has no verified image for every deployed platform"
			return
		}
	}
	row.VersionStatus = "available"
	row.Candidate = candidate
	row.VersionMessage = fmt.Sprintf("Patch tag available in %s%s.%s.x%s; review release notes before deployment", current.Prefix, current.Major, current.Minor, current.Suffix)
}

func tagFromImage(ref string) string { return ref[strings.LastIndex(ref, ":")+1:] }
