package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type registryTagSession struct {
	config                        Config
	registry, repo, authorization string
	client                        *http.Client
	credential                    func(context.Context, string) (tagCredential, error)
	usedCredential                bool
}

func parseTagChallenge(value string) (string, map[string]string, error) {
	if len(value) > 4096 {
		return "", nil, errors.New("Invalid registry challenge")
	}
	scheme, params, _ := strings.Cut(strings.TrimSpace(value), " ")
	quoted, escape := false, false
	var normalized strings.Builder
	for _, c := range params {
		if escape {
			escape = false
			normalized.WriteRune(c)
			continue
		}
		if c == '\\' && quoted {
			escape = true
			normalized.WriteRune(c)
			continue
		}
		if c == '"' {
			quoted = !quoted
		}
		if c == ',' && !quoted {
			c = ';'
		}
		normalized.WriteRune(c)
	}
	if quoted || escape {
		return "", nil, errors.New("Invalid registry challenge")
	}
	_, fields, e := mime.ParseMediaType(strings.ToLower(scheme) + ";" + normalized.String())
	return strings.ToLower(scheme), fields, e
}
func (s *registryTagSession) request(ctx context.Context, endpoint, authorization string, limit int64) ([]byte, http.Header, int, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return nil, nil, 0, tagError("unsupported_registry", "Unsupported registry endpoint")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, e := s.client.Do(req)
	if e != nil {
		var classified *registryTagError
		if errors.As(e, &classified) {
			return nil, nil, 0, classified
		}
		return nil, nil, 0, tagError("registry_unavailable", "Registry unavailable; check HTTPS connectivity and certificate trust")
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil || int64(len(raw)) > limit {
		return nil, nil, 0, tagError("registry_unavailable", "Registry response unavailable or too large")
	}
	return raw, resp.Header, resp.StatusCode, nil
}
func registryResponseError(status int) error {
	switch status {
	case 401, 403:
		return tagError("authentication_required", "Registry authentication required or the configured Docker login cannot list this repository")
	case 429:
		return tagError("rate_limited", "Registry request limit reached; try again later")
	case 404, 405:
		return tagError("unsupported_registry", "Repository not found or this registry does not expose the V2 tag-list API")
	default:
		return tagError("registry_unavailable", "Registry tag list unavailable")
	}
}
func basicTagAuthorization(c tagCredential) string {
	r, _ := http.NewRequest("GET", "https://registry.invalid", nil)
	r.SetBasicAuth(c.Username, c.Password)
	return r.Header.Get("Authorization")
}
func (s *registryTagSession) authorize(ctx context.Context, header string) error {
	scheme, params, e := parseTagChallenge(header)
	if e != nil {
		return tagError("authentication_required", "Unsupported registry authentication challenge")
	}
	if scheme == "basic" {
		if s.usedCredential {
			return registryResponseError(401)
		}
		credential, e := s.credential(ctx, s.registry)
		if e != nil {
			return e
		}
		s.authorization = basicTagAuthorization(credential)
		s.usedCredential = true
		return nil
	}
	if scheme != "bearer" {
		return tagError("authentication_required", "This registry authentication scheme is not supported for tag browsing")
	}
	realm, e := registryRealm(params["realm"])
	if e != nil || !trustedTagRealm(s.config, s.registry, realm) {
		return tagError("registry_not_authorized", "Registry token endpoint requires an exact registry_tag_auth_realms entry on this host")
	}
	if len(params["service"]) > 256 {
		return tagError("authentication_required", "Invalid registry token service")
	}
	q := url.Values{"scope": {"repository:" + s.repo + ":pull"}}
	if params["service"] != "" {
		q.Set("service", params["service"])
	}
	realm.RawQuery = q.Encode()
	tokenAuthorization := ""
	if s.authorization != "" {
		if s.usedCredential {
			return registryResponseError(401)
		}
		credential, e := s.credential(ctx, s.registry)
		if e != nil {
			return e
		}
		tokenAuthorization = basicTagAuthorization(credential)
		s.usedCredential = true
	}
	raw, _, status, e := s.request(ctx, realm.String(), tokenAuthorization, 64<<10)
	if e != nil {
		return e
	}
	if (status == 401 || status == 403) && !s.usedCredential {
		credential, e := s.credential(ctx, s.registry)
		if e != nil {
			return e
		}
		s.usedCredential = true
		raw, _, status, e = s.request(ctx, realm.String(), basicTagAuthorization(credential), 64<<10)
		if e != nil {
			return e
		}
	}
	if status != 200 {
		return registryResponseError(status)
	}
	var token struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(raw, &token) != nil {
		return tagError("authentication_required", "Registry token response is invalid")
	}
	if token.Token == "" {
		token.Token = token.AccessToken
	}
	if token.Token == "" || len(token.Token) > 16384 || strings.ContainsAny(token.Token, "\r\n ") || token.AccessToken != "" && token.AccessToken != token.Token {
		return tagError("authentication_required", "Registry token response is invalid")
	}
	s.authorization = "Bearer " + token.Token
	return nil
}
func (s *registryTagSession) tags(ctx context.Context, cursor string, size int) ([]string, bool, error) {
	q := url.Values{"n": {strconv.Itoa(size)}}
	if cursor != "" {
		q.Set("last", cursor)
	}
	endpoint := "https://" + s.registry + "/v2/" + s.repo + "/tags/list?" + q.Encode()
	for attempt := 0; attempt < 3; attempt++ {
		raw, headers, status, e := s.request(ctx, endpoint, s.authorization, 2<<20)
		if e != nil {
			return nil, false, e
		}
		if status == 401 {
			if e = s.authorize(ctx, headers.Get("WWW-Authenticate")); e != nil {
				return nil, false, e
			}
			continue
		}
		if status != 200 {
			return nil, false, registryResponseError(status)
		}
		var data struct {
			Name string          `json:"name"`
			Tags json.RawMessage `json:"tags"`
		}
		var tags []string
		if json.Unmarshal(raw, &data) != nil || data.Name != s.repo || len(data.Tags) == 0 || json.Unmarshal(data.Tags, &tags) != nil || len(tags) > size {
			return nil, false, tagError("registry_unavailable", "Registry returned an invalid tag page")
		}
		more := strings.Contains(headers.Get("Link"), `rel="next"`) || len(tags) == size
		if len(tags) == 0 && more {
			return nil, false, tagError("registry_unavailable", "Registry tag pagination did not advance")
		}
		return tags, more, nil
	}
	return nil, false, registryResponseError(401)
}
func genericRegistryTagPage(ctx context.Context, c Config, registry, repo, search, cursor string, page int, client *http.Client, credential func(context.Context, string) (tagCredential, error)) (imageTagPage, error) {
	result := imageTagPage{Tags: []string{}, Total: -1, Page: page}
	host, e := canonicalRegistryHost(registry)
	if e != nil || host != registry || len(repo) > 255 || !repositoryPattern.MatchString(repo) || strings.Contains(repo, ":") || page < 1 || page > 1000000 || len(search) > 128 || (search != "" && !tagPattern.MatchString(search)) || (cursor != "" && !tagPattern.MatchString(cursor)) {
		return result, tagError("unsupported_registry", "Invalid registry tag reference or search")
	}
	if client == nil {
		client, e = registryTagHTTPClient(c)
		if e != nil {
			return result, e
		}
	}
	safe := *client
	safe.Timeout = 10 * time.Second
	safe.Jar = nil
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if credential == nil {
		credential = loadRegistryTagCredential
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	session := &registryTagSession{config: c, registry: registry, repo: repo, client: &safe, credential: credential}
	seen := map[string]bool{}
	size := 50
	if search != "" {
		size = 1000
	}
	for scan := 0; scan < 4; scan++ {
		tags, more, e := session.tags(ctx, cursor, size)
		if e != nil {
			return result, e
		}
		for _, tag := range tags {
			if !tagPattern.MatchString(tag) || tag == cursor || seen[tag] {
				return result, tagError("registry_unavailable", "Registry returned duplicate or invalid tag cursors")
			}
			seen[tag] = true
		}
		for i, tag := range tags {
			cursor = tag
			if search == "" || strings.Contains(strings.ToLower(tag), strings.ToLower(search)) {
				result.Tags = append(result.Tags, tag)
			}
			if len(result.Tags) == 50 {
				result.More = i < len(tags)-1 || more
				result.NextCursor = cursor
				return result, nil
			}
		}
		result.More = more
		result.NextCursor = cursor
		if !more {
			return result, nil
		}
	}
	return result, nil
}
