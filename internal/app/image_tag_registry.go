package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func imageTagParts(ref string) (string, string) {
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}
func browserImageReference(ref string) (provider, repo, tag string, ok bool) {
	if !validCatalogReference(ref, false) || strings.Contains(ref, "@") {
		return
	}
	base, tag := imageTagParts(ref)
	if strings.HasPrefix(base, "lscr.io/") {
		repo = strings.TrimPrefix(base, "lscr.io/")
		parts := strings.Split(repo, "/")
		if len(parts) == 2 && parts[0] == "linuxserver" && repositoryPattern.MatchString(repo) {
			return "lscr", repo, tag, true
		}
	}
	hubRepo, hubTag, hubOK := hubVersionReference(base + ":" + tag)
	if hubOK {
		return "hub", hubRepo, hubTag, true
	}
	parts := strings.SplitN(base, "/", 2)
	if len(parts) == 2 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		host, e := canonicalRegistryHost(parts[0])
		if e == nil && len(parts[1]) <= 255 && repositoryPattern.MatchString(parts[1]) {
			return "v2:" + host, parts[1], tag, true
		}
	}
	return
}

// Only these two fixed HTTPS origins receive anonymous LSCR catalogue traffic.
// Challenges, redirects and pagination URLs never choose a destination.
func publicLSCRTagPage(ctx context.Context, repo, search, cursor string, page int, client *http.Client) (imageTagPage, error) {
	result := imageTagPage{Tags: []string{}, Page: page, Total: -1}
	if !strings.HasPrefix(repo, "linuxserver/") || len(strings.Split(repo, "/")) != 2 || !repositoryPattern.MatchString(repo) || page < 1 || page > 1000000 || len(search) > 128 || (search != "" && !tagPattern.MatchString(search)) || (cursor != "" && !tagPattern.MatchString(cursor)) {
		return result, errors.New("Invalid LinuxServer tag search")
	}
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		defer transport.CloseIdleConnections()
		client = &http.Client{Transport: transport}
	}
	safe := *client
	safe.Timeout = 10 * time.Second
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	safe.Jar = nil
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	moreHeader := false
	get := func(endpoint, token string, limit int64) ([]byte, error) {
		req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if e != nil {
			return nil, errors.New("Cannot prepare registry request")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, e := safe.Do(req)
		if e != nil {
			return nil, errors.New("LinuxServer tags unavailable")
		}
		defer resp.Body.Close()
		moreHeader = strings.Contains(resp.Header.Get("Link"), `rel="next"`)
		raw, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if e != nil || int64(len(raw)) > limit || resp.StatusCode != 200 {
			return nil, errors.New("LinuxServer tags unavailable, private or rate limited")
		}
		return raw, nil
	}
	raw, e := get("https://ghcr.io/token?"+url.Values{"service": {"ghcr.io"}, "scope": {"repository:" + repo + ":pull"}}.Encode(), "", 64<<10)
	if e != nil {
		return result, e
	}
	var auth struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &auth) != nil || auth.Token == "" || len(auth.Token) > 16384 || strings.ContainsAny(auth.Token, "\r\n") {
		return result, errors.New("Anonymous LinuxServer access unavailable")
	}
	size := "50"
	limit := 50
	if search != "" {
		size = "1000"
		limit = 1000
	}
	seen := map[string]bool{}
	for scan := 0; scan < 4; scan++ {
		params := url.Values{"n": {size}}
		if cursor != "" {
			params.Set("last", cursor)
		}
		raw, e = get("https://lscr.io/v2/"+repo+"/tags/list?"+params.Encode(), auth.Token, 2<<20)
		if e != nil {
			return result, e
		}
		var data struct {
			Name string          `json:"name"`
			Tags json.RawMessage `json:"tags"`
		}
		var tags []string
		if json.Unmarshal(raw, &data) != nil || data.Name != repo || len(data.Tags) == 0 || json.Unmarshal(data.Tags, &tags) != nil || len(tags) > limit {
			return result, errors.New("Invalid LinuxServer tag page")
		}
		full := len(tags) == limit || moreHeader
		if len(tags) == 0 && full {
			return result, errors.New("LinuxServer pagination did not advance")
		}
		for _, tag := range tags {
			if !tagPattern.MatchString(tag) || tag == cursor || seen[tag] {
				return result, errors.New("Inconsistent LinuxServer tag cursor")
			}
			seen[tag] = true
		}
		for i, tag := range tags {
			cursor = tag
			if search == "" || strings.Contains(strings.ToLower(tag), strings.ToLower(search)) {
				result.Tags = append(result.Tags, tag)
			}
			if len(result.Tags) == 50 {
				result.More = i < len(tags)-1 || full
				result.NextCursor = cursor
				return result, nil
			}
		}
		result.More = full
		result.NextCursor = cursor
		if !result.More {
			return result, nil
		}
	}
	// The next explicit page continues the bounded search, including empty segments.
	return result, nil
}
