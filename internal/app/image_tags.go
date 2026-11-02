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
	"strconv"
	"strings"
	"time"
)

type imageTagPage struct {
	NextCursor string   `json:"next_cursor,omitempty"`
	Tags       []string `json:"tags"`
	Total      int      `json:"total"`
	Page       int      `json:"page"`
	More       bool     `json:"more"`
	Image      string   `json:"image"`
	Revision   string   `json:"revision"`
}

// A manual page request never follows the registry's pagination URL or sends credentials.
func publicHubTagPage(ctx context.Context, repo, search string, page int, client *http.Client) (imageTagPage, error) {
	result := imageTagPage{Tags: []string{}, Page: page}
	if !repositoryPattern.MatchString(repo) || len(strings.Split(repo, "/")) != 2 || page < 1 || page > 1000000 || len(search) > 128 || (search != "" && !tagPattern.MatchString(search)) {
		return result, errors.New("Invalid tag search")
	}
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		defer transport.CloseIdleConnections()
		client = &http.Client{Transport: transport}
	}
	safe := *client
	safe.Jar = nil
	safe.Timeout = 10 * time.Second
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	parts := strings.Split(repo, "/")
	endpoint := "https://hub.docker.com/v2/namespaces/" + url.PathEscape(parts[0]) + "/repositories/" + url.PathEscape(parts[1]) + "/tags?" + url.Values{"page_size": {"50"}, "page": {strconv.Itoa(page)}, "name": {search}}.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	resp, err := safe.Do(req)
	if err != nil {
		return result, tagError("registry_unavailable", "Docker Hub tags unavailable")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return result, tagError("registry_unavailable", "Docker Hub response unavailable or too large")
	}
	if resp.StatusCode != 200 {
		return result, registryResponseError(resp.StatusCode)
	}
	var data struct {
		Count   *int    `json:"count"`
		Next    *string `json:"next"`
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &data) != nil || data.Count == nil || *data.Count < 0 || data.Results == nil || len(data.Results) > 50 {
		return result, errors.New("Invalid Docker Hub tag page")
	}
	seen := map[string]bool{}
	for _, row := range data.Results {
		if !tagPattern.MatchString(row.Name) || seen[row.Name] {
			return result, errors.New("Invalid Docker Hub tag metadata")
		}
		seen[row.Name] = true
		result.Tags = append(result.Tags, row.Name)
	}
	result.Total = *data.Count
	result.More = data.Next != nil && *data.Next != ""
	if result.More && len(result.Tags) == 0 || result.Total < len(result.Tags) {
		return imageTagPage{}, errors.New("Incomplete Docker Hub tag page")
	}
	return result, nil
}

// Reserve the saved project while resolving its service and preparing a consistent result.
func (m *Manager) withImageTagProject(ctx context.Context, id, service, rev string, run func(Project, string) (any, error)) (any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.Projects[id]
	if !ok || m.Images == nil {
		return nil, errors.New("Project image service unavailable")
	}
	p.mu.Lock()
	if m.agentUpdating || p.busy || p.ImageUpdates.Checking || rev == "" {
		p.mu.Unlock()
		return nil, errors.New("Wait for project operations and image checks to finish")
	}
	p.busy = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.busy = false; p.mu.Unlock() }()
	if err := sameRevision(p.Project, rev); err != nil {
		return nil, err
	}
	raw, err := m.Images.command(ctx, p.Project, composeArgs(p.Project, "", "config", "--format", "json")...)
	if err != nil {
		return nil, err
	}
	var config struct {
		Services map[string]struct {
			Image string
			Build json.RawMessage
		}
	}
	if json.Unmarshal(raw, &config) != nil {
		return nil, errors.New("Cannot read saved Compose services")
	}
	target, ok := config.Services[service]
	if !ok || len(target.Build) > 0 && string(target.Build) != "null" {
		return nil, errors.New("Choose a saved service using a registry image")
	}
	if _, _, _, ok := browserImageReference(target.Image); !ok {
		return nil, errors.New("Unsupported image reference for registry tag browsing")
	}
	result, err := run(p.Project, target.Image)
	if err != nil {
		return nil, err
	}
	if err := sameRevision(p.Project, rev); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *Server) imageTags(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	rev, service, id := r.FormValue("revision"), r.FormValue("service"), r.FormValue("id")
	if r.URL.Path == "/api/image-tags/prepare" && r.FormValue("confirmed") != "yes" {
		http.Error(w, "Explicit confirmation required", 400)
		return
	}
	result, err := s.m.withImageTagProject(ctx, id, service, rev, func(p Project, old string) (any, error) {
		provider, repo, oldTag, _ := browserImageReference(old)
		if r.URL.Path == "/api/image-tags" {
			page := 1
			if value := r.URL.Query().Get("page"); value != "" {
				n, e := strconv.Atoi(value)
				if e != nil {
					return nil, errors.New("Invalid tag page")
				}
				page = n
			}
			if strings.HasPrefix(provider, "v2:") {
				lookup := s.m.Images.V2TagPage
				if lookup == nil {
					lookup = func(ctx context.Context, host, repo, search, cursor string, page int) (imageTagPage, error) {
						return genericRegistryTagPage(ctx, s.cfg, host, repo, search, cursor, page, nil, nil)
					}
				}
				result, e := lookup(ctx, strings.TrimPrefix(provider, "v2:"), repo, r.URL.Query().Get("search"), r.URL.Query().Get("cursor"), page)
				result.Image = old
				result.Revision = rev
				return result, e
			}
			if provider == "lscr" {
				lookup := s.m.Images.RegistryTagPage
				if lookup == nil {
					lookup = func(ctx context.Context, repo, search, cursor string, page int) (imageTagPage, error) {
						return publicLSCRTagPage(ctx, repo, search, cursor, page, nil)
					}
				}
				result, e := lookup(ctx, repo, r.URL.Query().Get("search"), r.URL.Query().Get("cursor"), page)
				result.Image = old
				result.Revision = rev
				return result, e
			}
			lookup := s.m.Images.TagPage
			if lookup == nil {
				lookup = func(ctx context.Context, repo, query string, page int) (imageTagPage, error) {
					return publicHubTagPage(ctx, repo, query, page, nil)
				}
			}
			var result imageTagPage
			var e error
			if r.URL.Query().Get("cursor") == "" || s.m.Images.TagPage != nil {
				result, e = lookup(ctx, repo, r.URL.Query().Get("search"), page)
			}
			var classified *registryTagError
			fallback := errors.As(e, &classified) && (classified.Code == "authentication_required" || classified.Code == "unsupported_registry")
			if (fallback || r.URL.Query().Get("cursor") != "") && s.m.Images.TagPage == nil {
				result, e = genericRegistryTagPage(ctx, s.cfg, "registry-1.docker.io", repo, r.URL.Query().Get("search"), r.URL.Query().Get("cursor"), page, nil, nil)
			}
			result.Image = old
			result.Revision = rev
			return result, e
		}
		next := r.FormValue("candidate")
		nextProvider, nextRepo, nextTag, ok := browserImageReference(next)
		if !ok || nextProvider != provider || nextRepo != repo || nextTag == oldTag {
			return nil, errors.New("Choose a different tag in the same registry and repository")
		}
		// Preserve the original repository spelling; never redirect registry credentials.
		base, _ := imageTagParts(old)
		next = base + ":" + nextTag
		containers, e := s.m.Images.containers(ctx, p)
		if e != nil {
			return nil, e
		}
		platforms := []imagePlatform{}
		for _, c := range containers {
			if c.Service != service {
				continue
			}
			image, e := s.m.Images.image(ctx, p, c.ImageID)
			if e != nil {
				return nil, e
			}
			platforms = append(platforms, image.imagePlatform)
		}
		if len(platforms) == 0 {
			return nil, errors.New("No deployed image platform can be verified for this service; edit Compose manually")
		}
		verifier := &ImageService{Exec: s.m.Images.Exec}
		manifests, e := verifier.manifests(ctx, p, next)
		if e != nil {
			return nil, e
		}
		for _, platform := range platforms {
			if _, e = remoteImageID(manifests, platform); e != nil {
				return nil, errors.New("Selected tag has no verified image for every deployed platform")
			}
		}
		raw, e := os.ReadFile(filepath.Join(p.Directory, p.ComposeFiles[0]))
		if e != nil || len(raw) > 1<<20 {
			return nil, errors.New("Cannot read main Compose file within editing limit")
		}
		content, e := replaceLiteralImage(string(raw), old, next)
		if e != nil {
			return nil, e
		}
		if e = s.m.Images.validateImageProposal(ctx, p, service, old, next, content); e != nil {
			return nil, e
		}
		return map[string]string{"content": content, "revision": rev}, nil
	})
	if err != nil {
		var classified *registryTagError
		if errors.As(err, &classified) {
			w.Header().Set("X-Reef-Registry-Error", classified.Code)
		}
		http.Error(w, err.Error(), 409)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, result)
}
