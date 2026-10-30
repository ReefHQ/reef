package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"reef/internal/buildinfo"
)

const reefTagsURL = "https://api.github.com/repos/ReefHQ/reef/tags"
const reefUpdateInterval = 6 * time.Hour

var reefStableVersion = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Compare decimal components as strings, avoiding overflow and lexical ordering.
func compareReefVersions(a, b string) (int, bool) {
	aa, bb := reefStableVersion.FindStringSubmatch(a), reefStableVersion.FindStringSubmatch(b)
	if aa == nil || bb == nil {
		return 0, false
	}
	for i := 1; i <= 3; i++ {
		if len(aa[i]) < len(bb[i]) {
			return -1, true
		}
		if len(aa[i]) > len(bb[i]) {
			return 1, true
		}
		if c := strings.Compare(aa[i], bb[i]); c != 0 {
			return c, true
		}
	}
	return 0, true
}

type reefUpdateStatus struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	Available bool      `json:"available"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	URL       string    `json:"url,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	Checking  bool      `json:"checking"`
}
type reefUpdateChecker struct {
	mu        sync.Mutex
	client    *http.Client
	status    reefUpdateStatus
	attempted time.Time
}

func newReefUpdateChecker() *reefUpdateChecker {
	return &reefUpdateChecker{client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, status: reefUpdateStatus{Current: buildinfo.Version, Status: "unknown", Message: "Update check has not completed yet."}}
}
func (c *reefUpdateChecker) snapshot() reefUpdateStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}
func (c *reefUpdateChecker) latest(ctx context.Context) (string, error) {
	latest := ""
	seen := map[string]bool{}
	for page := 1; page <= 5; page++ {
		req, err := http.NewRequestWithContext(ctx, "GET", reefTagsURL+"?per_page=100&page="+strconv.Itoa(page), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "Reef-update-check")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := c.client.Do(req)
		if err != nil {
			return "", err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
		resp.Body.Close()
		if resp.StatusCode != 200 || readErr != nil || len(data) > 2*1024*1024 {
			return "", errors.New("GitHub tags unavailable")
		}
		var tags []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &tags) != nil || tags == nil || len(tags) > 100 {
			return "", errors.New("Invalid GitHub tags")
		}
		for _, tag := range tags {
			if tag.Name == "" || seen[tag.Name] {
				return "", errors.New("Inconsistent GitHub tag pagination")
			}
			seen[tag.Name] = true
			if len(tag.Name) > 128 {
				continue
			}
			if _, ok := compareReefVersions(tag.Name, "0.0.0"); !ok || !strings.HasPrefix(tag.Name, "v") {
				continue
			}
			if latest == "" {
				latest = tag.Name
			} else if cmp, _ := compareReefVersions(tag.Name, latest); cmp > 0 {
				latest = tag.Name
			}
		}
		// Do not follow response URLs: only fixed-origin, locally generated pages.
		if len(tags) < 100 {
			if latest == "" {
				return "", errors.New("No stable tags")
			}
			return latest, nil
		}
	}
	return "", errors.New("GitHub tag search exceeded its limit")
}
func (c *reefUpdateChecker) check(ctx context.Context) {
	c.mu.Lock()
	if c.status.Checking || (!c.attempted.IsZero() && time.Since(c.attempted) < time.Minute) {
		c.mu.Unlock()
		return
	}
	c.attempted = time.Now()
	c.status.Checking = true
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	latest, err := c.latest(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status.Checking = false
	c.status.CheckedAt = time.Now().UTC()
	if err != nil {
		// Keep an already discovered update visible, but mark the observation stale.
		c.status.Status = "unknown"
		c.status.Message = "Could not check GitHub. Any previously detected version is from an earlier check."
		return
	}
	c.status.Latest = strings.TrimPrefix(latest, "v")
	c.status.URL = "https://github.com/ReefHQ/reef/blob/" + latest + "/CHANGELOG.md"
	cmp, ok := compareReefVersions(latest, c.status.Current)
	c.status.Available = ok && cmp > 0
	switch {
	case !ok:
		c.status.Status = "development"
		c.status.Message = "This development or prerelease build cannot be compared with stable tags."
	case cmp > 0:
		c.status.Status = "available"
		c.status.Message = fmt.Sprintf("Reef %s is available on GitHub.", c.status.Latest)
	case cmp < 0:
		c.status.Status = "ahead"
		c.status.Message = "This Reef version is newer than the latest public stable tag."
	default:
		c.status.Status = "current"
		c.status.Message = "You are running the latest public stable version."
	}
}
func (s *Server) runReefUpdateChecks(ctx context.Context) {
	go func() {
		s.reefUpdates.check(ctx)
		ticker := time.NewTicker(reefUpdateInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reefUpdates.check(ctx)
			}
		}
	}()
}
func (s *Server) reefUpdateAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		s.reefUpdates.check(r.Context())
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(s.reefUpdates.snapshot())
}
