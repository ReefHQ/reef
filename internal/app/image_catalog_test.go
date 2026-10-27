package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"reef/internal/agents"
)

type catalogExecutor struct {
	mu      sync.Mutex
	rows    []CatalogImage
	used    map[string]bool
	calls   [][]string
	fail    bool
	moved   bool
	gate    chan struct{}
	entered chan struct{}
}

func catalogFixture() *catalogExecutor {
	return &catalogExecutor{rows: []CatalogImage{{ID: oldImage, Tags: []string{"example/app:one", "example/app:two"}, Digests: []string{}, Created: "2026-10-27T00:00:00Z", Size: 123, OS: "linux", Architecture: "amd64"}, {ID: newImage, Tags: []string{}, Digests: []string{}, Created: "2026-10-26T00:00:00Z", Size: 321, OS: "linux", Architecture: "arm64"}}, used: map[string]bool{}}
}
func (x *catalogExecutor) Run(ctx context.Context, p Project, args []string, w io.Writer) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.calls = append(x.calls, append([]string{}, args...))
	emit := func(value any) error { return json.NewEncoder(w).Encode(value) }
	if x.fail {
		io.WriteString(w, "registry-password-secret")
		return errors.New("registry-password-secret")
	}
	if args[0] == "image" && args[1] == "pull" {
		if x.entered != nil {
			close(x.entered)
			select {
			case <-x.gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	switch strings.Join(args[:2], " ") {
	case "image ls":
		for _, row := range x.rows {
			io.WriteString(w, row.ID+"\n")
		}
		return nil
	case "container ls":
		if len(args) > 5 {
			if x.used[args[len(args)-1][len("ancestor="):]] {
				io.WriteString(w, oldContainer)
			}
		} else {
			for _, used := range x.used {
				if used {
					io.WriteString(w, oldContainer+"\n")
				}
			}
		}
		return nil
	case "container inspect":
		for id, used := range x.used {
			if used {
				return emit(map[string]string{"id": oldContainer, "image": id, "name": "/stopped-private-container"})
			}
		}
		return nil
	case "image inspect":
		for _, ref := range args[4:] {
			found := false
			for _, row := range x.rows {
				match := row.ID == ref
				for _, tag := range row.Tags {
					match = match || tag == ref
				}
				if match {
					found = true
					if args[3] == "{{.Id}}" {
						id := row.ID
						if x.moved {
							id = newImage
						}
						io.WriteString(w, id+"\n")
					} else {
						emit(row)
					}
					break
				}
			}
			if !found {
				return errors.New("missing image")
			}
		}
		return nil
	case "image tag":
		for i := range x.rows {
			if x.rows[i].ID == args[2] {
				x.rows[i].Tags = append(x.rows[i].Tags, args[3])
				return nil
			}
		}
		return errors.New("missing image")
	case "image rm":
		ref := args[len(args)-1]
		for i := range x.rows {
			row := &x.rows[i]
			if row.ID == ref {
				x.rows = append(x.rows[:i], x.rows[i+1:]...)
				return nil
			}
			for j, tag := range row.Tags {
				if tag == ref {
					row.Tags = append(row.Tags[:j], row.Tags[j+1:]...)
					if len(row.Tags) == 0 {
						x.rows = append(x.rows[:i], x.rows[i+1:]...)
					}
					return nil
				}
			}
		}
		return errors.New("missing image")
	}
	return errors.New("unexpected command")
}
func catalogTargets(t *testing.T, s *ImageService) []ImageTarget {
	t.Helper()
	rows, err := s.catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := []ImageTarget{}
	for _, row := range rows {
		out = append(out, ImageTarget{row.ID, row.Fingerprint})
	}
	return out
}
func TestCatalogReferences(t *testing.T) {
	for _, ref := range []string{"alpine", "alpine:3.22", "registry.test:5000/team/app:release_1", "ghcr.io/reefhq/reef:v0.7.0", "repo@" + oldImage} {
		if !validCatalogReference(ref, false) {
			t.Fatal(ref)
		}
	}
	for _, ref := range []string{"--help", "https://registry/app:tag", "user:password@registry/app", "../app:tag", "app:", "UPPER/app:tag", "app@latest", "app;rm", "app\nother", oldImage} {
		if validCatalogReference(ref, false) {
			t.Fatal("accepted", ref)
		}
	}
	if validCatalogReference("alpine", true) || validCatalogReference("repo@"+oldImage, true) {
		t.Fatal("explicit tag required")
	}
}
func TestCatalogInventoryAndSafeBatchDeletion(t *testing.T) {
	x := catalogFixture()
	x.used[newImage] = true
	s := &ImageService{Exec: x}
	rows, err := s.catalog(context.Background())
	if err != nil || len(rows) != 2 || !reflect.DeepEqual(rows[1].Containers, []string{"stopped-private-container"}) {
		t.Fatal(rows, err)
	}
	targets := catalogTargets(t, s)
	results := s.runCatalog(context.Background(), "cleanup", "", "", targets)
	if !results[0].OK || results[1].OK || len(x.rows) != 1 || x.rows[0].ID != newImage {
		t.Fatal(results, x.rows)
	}
	for _, call := range x.calls {
		command := strings.Join(call, " ")
		if strings.Contains(command, "--force") || strings.Contains(command, "image prune") || strings.HasPrefix(command, "system") {
			t.Fatal(command)
		}
	}
	// Both explicit tags were removed individually, with no parent pruning.
	deletes := 0
	for _, call := range x.calls {
		if call[1] == "rm" {
			deletes++
			if call[2] != "--no-prune" {
				t.Fatal(call)
			}
		}
	}
	if deletes != 2 {
		t.Fatal(deletes)
	}
}
func TestCatalogChangedTagsAndMovedReferenceAreRetained(t *testing.T) {
	for _, mode := range []string{"fingerprint", "moved", "usage"} {
		t.Run(mode, func(t *testing.T) {
			x := catalogFixture()
			s := &ImageService{Exec: x}
			target := catalogTargets(t, s)[0]
			if mode == "fingerprint" {
				x.rows[0].Tags = append(x.rows[0].Tags, "app:changed")
			}
			if mode == "moved" {
				x.moved = true
			}
			if mode == "usage" {
				x.used[oldImage] = true
			}
			if err := s.mutateCatalogImage(context.Background(), "remove", "", target); err == nil {
				t.Fatal("changed image removed")
			}
			for _, args := range x.calls {
				if args[1] == "rm" {
					t.Fatal(args)
				}
			}
		})
	}
}
func TestCatalogTaggingAndLastUntag(t *testing.T) {
	x := catalogFixture()
	s := &ImageService{Exec: x}
	target := catalogTargets(t, s)[0]
	if err := s.mutateCatalogImage(context.Background(), "tag", "example/app:three", target); err != nil {
		t.Fatal(err)
	}
	target = catalogTargets(t, s)[0]
	if err := s.mutateCatalogImage(context.Background(), "tag", "example/app:three", target); err == nil {
		t.Fatal("existing destination replaced")
	}
	for _, tag := range []string{"example/app:one", "example/app:two", "example/app:three"} {
		target = catalogTargets(t, s)[0]
		if err := s.mutateCatalogImage(context.Background(), "untag", tag, target); err != nil {
			t.Fatal(err)
		}
	}
	if len(x.rows) != 1 || x.rows[0].ID != newImage {
		t.Fatal(x.rows)
	}
}
func TestCatalogWorkerLocksAndSanitizedFailures(t *testing.T) {
	x := catalogFixture()
	x.gate = make(chan struct{})
	x.entered = make(chan struct{})
	m, p, rev := fixture(t, &fakeExecutor{})
	m.Images = &ImageService{Exec: x}
	op, err := m.beginCatalog("pull", "example/app:one", "", "")
	if err != nil || !op.Running {
		t.Fatal(op, err)
	}
	<-x.entered
	if _, err := m.beginCatalog("pull", "example/app:two", "", ""); err == nil {
		t.Fatal("duplicate worker")
	}
	if err := m.begin(p.ID, "stop", rev, ""); err == nil {
		t.Fatal("Compose overlapped image mutation")
	}
	close(x.gate)
	m.wg.Wait()
	if op := m.Images.catalogOperation(); op.Running || len(op.Results) != 1 || !op.Results[0].OK {
		t.Fatal(op)
	}
	m.Projects[p.ID].busy = true
	if _, err := m.beginCatalog("pull", "app:tag", "", ""); err == nil {
		t.Fatal("image mutation overlapped Compose")
	}
	m.Projects[p.ID].busy = false
	x.fail = true
	results := m.Images.runCatalog(context.Background(), "pull", "app:tag", "", nil)
	if results[0].OK || strings.Contains(results[0].Message, "secret") {
		t.Fatal(results)
	}
}
func TestCatalogAPISecurityAndRemoteParity(t *testing.T) {
	local, _, _ := fixture(t, &fakeExecutor{})
	lx := catalogFixture()
	local.Images = &ImageService{Exec: lx}
	central := newServer(local, Config{AgentsEnabled: true}, "password-long-enough")
	central.agents, _ = agents.Open(t.TempDir())
	central.sessions[sessionKey("cookie")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "cookie"}
	remote, _, _ := fixture(t, &fakeExecutor{})
	rx := catalogFixture()
	rx.rows[0].Tags = []string{"remote:one"}
	remote.Images = &ImageService{Exec: rx}
	backend := newServer(remote, Config{}, "unused-password")
	backend.discovery = &inventoryStub{}
	host, stop := relayAgentFixture(t, central, backend, "remote")
	defer stop()
	h := central.Handler()
	if w := call(h, "GET", "/api/images?host="+host, "", "", cookie); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := central.agents.Images(host, "wrong-generation", true); err == nil {
		t.Fatal("accepted stale generation")
	}
	host, stopReady := relayAgentFixture(t, central, backend, "capable-remote", agents.ImagesCapability)
	defer stopReady()
	for _, tc := range []struct {
		method, path, body, origin string
		cookie                     *http.Cookie
		status                     int
	}{
		{"GET", "/api/images", "", "", nil, 401},
		{"POST", "/api/images/action", "csrf=csrf&action=pull&reference=app:tag", "http://evil.test", cookie, 403},
		{"POST", "/api/images/action", "csrf=wrong&action=pull&reference=app:tag", "http://reef.test", cookie, 403},
		{"GET", "/api/images/action", "", "", cookie, 405},
		{"POST", "/api/images/action", "csrf=csrf&action=prune", "http://reef.test", cookie, 409},
	} {
		w := call(h, tc.method, tc.path, tc.body, tc.origin, tc.cookie)
		if w.Code != tc.status {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	w := call(h, "GET", "/api/images?host="+host, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "remote:one") {
		t.Fatal(w.Code, w.Body.String())
	}
	body := url.Values{"csrf": {"csrf"}, "action": {"pull"}, "reference": {"example/remote:one"}}.Encode()
	w = call(h, "POST", "/api/images/action?host="+host, body, "http://reef.test", cookie)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	remote.wg.Wait()
	if len(lx.calls) != 0 {
		t.Fatal("remote request touched local Docker")
	}
	w = call(h, "GET", "/api/images/operation?host="+host, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Completed") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestCatalogBatchValidationAndMultipleSelection(t *testing.T) {
	x := catalogFixture()
	m, _, _ := fixture(t, &fakeExecutor{})
	m.Images = &ImageService{Exec: x}
	for _, raw := range []string{"[]", "not-json", `[{"id":"--force","fingerprint":"bad"}]`} {
		if _, err := m.beginCatalog("remove", "", "", raw); err == nil {
			t.Fatal("accepted invalid batch", raw)
		}
	}
	targets := catalogTargets(t, m.Images)
	duplicate, _ := json.Marshal([]ImageTarget{targets[0], targets[0]})
	if _, err := m.beginCatalog("cleanup", "", "", string(duplicate)); err == nil {
		t.Fatal("accepted duplicate")
	}
	raw, _ := json.Marshal(targets)
	if _, err := m.beginCatalog("remove", "", "", string(raw)); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	op := m.Images.catalogOperation()
	if len(op.Results) != 2 || !op.Results[0].OK || !op.Results[1].OK || len(x.rows) != 0 {
		t.Fatal(op, x.rows)
	}
}
