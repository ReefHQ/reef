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
	"sync"
	"testing"
	"time"
)

type buildExecutor struct {
	mu            sync.Mutex
	calls         [][]string
	failBuild     bool
	noBuild       bool
	gate, entered chan struct{}
	cache         []map[string]any
}

func buildFixtureExecutor() *buildExecutor {
	return &buildExecutor{cache: []map[string]any{{"ID": "cachefixture001", "Size": "2048", "CreatedAt": "2026-10-27T12:00:00Z", "LastUsedAt": "2026-10-27T12:00:00Z", "Reclaimable": true, "Mutable": false, "Shared": false, "Type": "regular", "UsageCount": 1, "Description": "private-dockerfile-secret"}}}
}
func (x *buildExecutor) Run(ctx context.Context, p Project, args []string, w io.Writer) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.calls = append(x.calls, append([]string{}, args...))
	emit := func(v any) error { return json.NewEncoder(w).Encode(v) }
	if args[0] == "buildx" {
		id := ""
		for _, arg := range args {
			if strings.HasPrefix(arg, "id=") {
				id = strings.TrimPrefix(arg, "id=")
			}
		}
		if args[1] == "du" {
			for _, row := range x.cache {
				if id == "" || row["ID"] == id {
					emit(row)
				}
			}
			return nil
		}
		if args[1] == "prune" {
			if id == "" {
				return errors.New("unfiltered prune refused by fixture")
			}
			for i, row := range x.cache {
				if row["ID"] == id {
					x.cache = append(x.cache[:i], x.cache[i+1:]...)
					break
				}
			}
			return nil
		}
	}
	for i, arg := range args {
		if arg == "config" {
			if i+1 < len(args) && args[i+1] == "--format" {
				build := any(map[string]any{"context": ".", "args": map[string]string{"PRIVATE": "secret-custom"}})
				if x.noBuild {
					build = nil
				}
				return emit(map[string]any{"services": map[string]any{"web": map[string]any{"build": build}, "db": map[string]any{"image": "postgres:17"}}})
			}
			return nil
		}
		if arg == "build" {
			if x.entered != nil {
				close(x.entered)
				select {
				case <-x.gate:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			io.WriteString(w, "STEP output secret-custom\n")
			if x.failBuild {
				return errors.New("injected build failure")
			}
			return nil
		}
		if arg == "down" || arg == "up" {
			return nil
		}
	}
	return errors.New("unexpected build fixture command")
}
func projectBuildFixture(t *testing.T) (*Manager, Project, string, *buildExecutor) {
	t.Helper()
	m, p, rev := fixture(t, &fakeExecutor{})
	x := buildFixtureExecutor()
	m.Exec = x
	m.Images = &ImageService{Exec: x}
	return m, p, rev, x
}
func TestBuildUsesSavedServicesOptionsAndNeverDeploys(t *testing.T) {
	m, p, rev, x := projectBuildFixture(t)
	before, _ := os.ReadFile(filepath.Join(p.Directory, "compose.yaml"))
	if err := m.beginOptions(p.ID, "build", rev, "unsaved invalid draft", BuildOptions{Service: "web", NoCache: true, Pull: true}); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	op := m.Projects[p.ID].Op
	if op.Phase != "completed" || op.NeedsStart || !strings.Contains(op.Log, "[redacted]") || strings.Contains(op.Log, "secret-custom") {
		t.Fatal(op)
	}
	found := false
	for _, args := range x.calls {
		cmd := strings.Join(args, " ")
		if strings.Contains(cmd, " build ") {
			found = true
			if !strings.Contains(cmd, "build --builder default --no-cache --pull -- web") {
				t.Fatal(cmd)
			}
		}
		if strings.Contains(cmd, " down") || strings.Contains(cmd, " up ") || strings.Contains(cmd, "--push") {
			t.Fatal(cmd)
		}
	}
	if !found {
		t.Fatal("no build command")
	}
	after, _ := os.ReadFile(filepath.Join(p.Directory, "compose.yaml"))
	if string(before) != string(after) {
		t.Fatal("build changed Compose file")
	}
	history, err := m.buildHistory(m.Projects[p.ID])
	if err != nil || len(history) != 1 || history[0].Log != "" {
		t.Fatal(history, err)
	}
	// A later non-build operation does not erase build history or its log.
	if err = m.begin(p.ID, "stop", rev, ""); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	history, err = m.buildHistory(m.Projects[p.ID])
	if err != nil || len(history) != 1 || history[0].ID != op.ID {
		t.Fatal(history, err)
	}
	archived, err := m.readBuildHistory(p.ID, p.ID+"-"+op.ID+".history.json")
	if err != nil || archived.Log != op.Log {
		t.Fatal(archived, err)
	}
}
func TestBuildLockFailureAndServiceValidation(t *testing.T) {
	m, p, rev, x := projectBuildFixture(t)
	x.entered = make(chan struct{})
	x.gate = make(chan struct{})
	if err := m.begin(p.ID, "build", rev, ""); err != nil {
		t.Fatal(err)
	}
	<-x.entered
	if _, err := m.beginCatalog("pull", "app:latest", "", ""); err == nil {
		t.Fatal("image mutation overlapped build")
	}
	if _, err := m.beginVolumes("create", "new-data", ""); err == nil {
		t.Fatal("volume mutation overlapped build")
	}
	if _, err := m.beginCacheCleanup([]CacheTarget{{"cachefixture001", strings.Repeat("a", 64)}}); err == nil {
		t.Fatal("cache prune overlapped build")
	}
	if err := m.begin(p.ID, "stop", rev, ""); err == nil {
		t.Fatal("Compose operation overlapped build")
	}
	close(x.gate)
	m.wg.Wait()
	x.entered = nil
	x.failBuild = true
	if err := m.beginOptions(p.ID, "build", rev, "", BuildOptions{Service: "db"}); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	if m.Projects[p.ID].Op.Phase != "failed" || m.Projects[p.ID].Op.NeedsStart {
		t.Fatal(m.Projects[p.ID].Op)
	}
	if err := m.beginOptions(p.ID, "build", rev, "", BuildOptions{Service: "web"}); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	if m.Projects[p.ID].Op.Phase != "failed" {
		t.Fatal("failed build reported success")
	}
	if err := m.begin(p.ID, "build", "stale", ""); err == nil {
		t.Fatal("stale revision accepted")
	}
}
func TestInterruptedBuildPreservesHistoryWithoutClaimingContainersStopped(t *testing.T) {
	m, p, _, _ := projectBuildFixture(t)
	op := Operation{ID: randomID(), Action: "build", Running: true, Started: time.Now(), Build: BuildOptions{Service: "web"}}
	m.Projects[p.ID].Op = op
	if err := m.persist(m.Projects[p.ID]); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newManager(Config{StateDir: m.StateDir, Projects: []Project{p}}, &fakeExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	current := reloaded.Projects[p.ID].Op
	if current.Running || current.NeedsStart || current.Phase != "interrupted" {
		t.Fatal(current)
	}
	rows, err := reloaded.buildHistory(reloaded.Projects[p.ID])
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
}
func TestCacheWhitelistFingerprintAndTargetedRemoval(t *testing.T) {
	x := buildFixtureExecutor()
	s := &ImageService{Exec: x}
	rows, err := s.cacheRecords(context.Background(), "")
	if err != nil || len(rows) != 1 || rows[0].Size != 2048 {
		t.Fatal(rows, err)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "private") {
		t.Fatal("cache description leaked")
	}
	target := CacheTarget{rows[0].ID, rows[0].Fingerprint}
	x.cache[0]["UsageCount"] = 2
	if err := s.removeCacheRecord(context.Background(), target); err == nil {
		t.Fatal("changed cache removed")
	}
	x.cache[0]["UsageCount"] = 1
	x.cache[0]["Mutable"] = true
	rows, _ = s.cacheRecords(context.Background(), "")
	if err := s.removeCacheRecord(context.Background(), CacheTarget{rows[0].ID, rows[0].Fingerprint}); err == nil {
		t.Fatal("mutable cache removed")
	}
	x.cache[0]["Mutable"] = false
	x.cache[0]["Reclaimable"] = false
	rows, _ = s.cacheRecords(context.Background(), "")
	if err := s.removeCacheRecord(context.Background(), CacheTarget{rows[0].ID, rows[0].Fingerprint}); err == nil {
		t.Fatal("in-use cache removed")
	}
	x.cache[0]["Reclaimable"] = true
	if err := s.removeCacheRecord(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	for _, args := range x.calls {
		if args[1] == "prune" {
			cmd := strings.Join(args, " ")
			for _, expected := range []string{"--builder default", "id=cachefixture001"} {
				if !strings.Contains(cmd, expected) {
					t.Fatal(cmd)
				}
			}
		}
	}
}
func TestBuildAPISecurityRemoteExecutionAndHistory(t *testing.T) {
	local, _, _, lx := projectBuildFixture(t)
	central := newServer(local, Config{AgentsEnabled: true}, "password-long-enough")
	central.agents, _ = agents.Open(t.TempDir())
	central.sessions[sessionKey("cookie")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "cookie"}
	remote, p, rev, _ := projectBuildFixture(t)
	backend := newServer(remote, Config{}, "unused-password")
	backend.discovery = &inventoryStub{}
	old, stopOld := relayAgentFixture(t, central, backend, "old", agents.ImagesCapability)
	defer stopOld()
	h := central.Handler()
	if w := call(h, "GET", "/api/builds?host="+old, "", "", cookie); w.Code != 409 {
		t.Fatal(w.Code)
	}
	host, stop := relayAgentFixture(t, central, backend, "build-agent", agents.BuildsCapability)
	defer stop()
	for _, tc := range []struct {
		method, path, body, origin string
		cookie                     *http.Cookie
		status                     int
	}{
		{"GET", "/api/builds", "", "", nil, 401}, {"POST", "/api/builds/action", "id=test&csrf=csrf&confirmed=yes", "http://evil.test", cookie, 403}, {"POST", "/api/builds/action", "id=test&csrf=wrong&confirmed=yes", "http://reef.test", cookie, 403}, {"POST", "/api/builds/action", "id=test&csrf=csrf", "http://reef.test", cookie, 400}, {"GET", "/api/builds/log?id=test&operation=../../secret", "", "", cookie, 400},
	} {
		w := call(h, tc.method, tc.path, tc.body, tc.origin, tc.cookie)
		if w.Code != tc.status {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	detail := call(h, "GET", "/api/builds/project?host="+host+"&id="+p.ID, "", "", cookie)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "web") || strings.Contains(detail.Body.String(), "secret-custom") {
		t.Fatal(detail.Code, detail.Body.String())
	}
	body := url.Values{"id": {p.ID}, "revision": {rev}, "csrf": {"csrf"}, "confirmed": {"yes"}, "service": {"web"}}.Encode()
	w := call(h, "POST", "/api/builds/action?host="+host, body, "http://reef.test", cookie)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	remote.wg.Wait()
	if len(lx.calls) > 0 {
		t.Fatal("remote build used Local")
	}
	history := call(h, "GET", "/api/builds/history?host="+host+"&id="+p.ID, "", "", cookie)
	if history.Code != 200 || strings.Contains(history.Body.String(), "STEP output") {
		t.Fatal(history.Code, history.Body.String())
	}
	op := remote.Projects[p.ID].Op
	log := call(h, "GET", "/api/builds/log?host="+host+"&id="+p.ID+"&operation="+op.ID, "", "", cookie)
	if log.Code != 200 || !strings.Contains(log.Body.String(), "[redacted]") {
		t.Fatal(log.Code, log.Body.String())
	}
	cache := call(h, "GET", "/api/builds/cache?host="+host, "", "", cookie)
	if cache.Code != 200 || strings.Contains(cache.Body.String(), "private-dockerfile") {
		t.Fatal(cache.Code, cache.Body.String())
	}
	var cacheData struct{ Records []BuildCacheRecord }
	if err := json.Unmarshal(cache.Body.Bytes(), &cacheData); err != nil || len(cacheData.Records) != 1 {
		t.Fatal(err, cache.Body.String())
	}
	targets, _ := json.Marshal([]CacheTarget{{cacheData.Records[0].ID, cacheData.Records[0].Fingerprint}})
	cacheBody := url.Values{"csrf": {"csrf"}, "confirmed": {"yes"}, "targets": {string(targets)}}.Encode()
	response := call(h, "POST", "/api/builds/cache/action?host="+host, cacheBody, "http://reef.test", cookie)
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	remote.wg.Wait()
	response = call(h, "GET", "/api/builds/cache/operation?host="+host, "", "", cookie)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Cache removed") {
		t.Fatal(response.Code, response.Body.String())
	}
	if len(lx.calls) > 0 {
		t.Fatal("remote cache mutation touched Local")
	}
	token := strings.Repeat("b", 48)
	secret := filepath.Join(t.TempDir(), "private")
	os.WriteFile(secret, []byte("do-not-expose"), 0600)
	os.Symlink(secret, filepath.Join(remote.StateDir, p.ID+"-"+token+".history.json"))
	response = call(h, "GET", "/api/builds/log?host="+host+"&id="+p.ID+"&operation="+token, "", "", cookie)
	if response.Code != 404 || strings.Contains(response.Body.String(), "do-not-expose") {
		t.Fatal(response.Code, response.Body.String())
	}

}
