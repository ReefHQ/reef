package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reef/internal/docker"
)

func canonicalTemp(t *testing.T) string {
	t.Helper()
	r, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func adoptionFixture(t *testing.T) (Config, Project) {
	t.Helper()
	root := canonicalTemp(t)
	dir := filepath.Join(root, "app")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"compose.yaml", "override.yaml", "custom.env"} {
		if e := os.WriteFile(filepath.Join(dir, name), []byte("services: {}\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	return Config{StateDir: canonicalTemp(t), AllowedProjectRoots: []string{root}}, Project{Name: "My existing app", ProjectName: "existing-app", Directory: dir, ComposeFiles: []string{"compose.yaml", "override.yaml"}, EnvFiles: []string{"custom.env"}}
}
func TestRegistrationPersistsWithoutExecutingOrAlteringProject(t *testing.T) {
	c, p := adoptionFixture(t)
	x := &fakeExecutor{}
	m, e := newManager(c, x)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(p.Directory, "compose.yaml"))
	saved, e := m.registerProject(p)
	if e != nil {
		t.Fatal(e)
	}
	if len(x.calls) != 0 || saved.ID == "" || len(m.snapshot()) != 1 {
		t.Fatal("registration executed Compose or was not activated")
	}
	after, _ := os.ReadFile(filepath.Join(p.Directory, "compose.yaml"))
	if string(before) != string(after) {
		t.Fatal("project file changed")
	}
	stat, e := os.Stat(filepath.Join(c.StateDir, "registered-projects.json"))
	if e != nil || stat.Mode().Perm() != 0600 {
		t.Fatal(stat, e)
	}
	// Existing config stays untouched; only the separate private registry persists new access.
	reloaded, e := newManager(c, x)
	if e != nil {
		t.Fatal(e)
	}
	got, ok := reloaded.get(saved.ID)
	if !ok || got.Project.ProjectName != p.ProjectName || strings.Join(got.Project.ComposeFiles, ",") != "compose.yaml,override.yaml" || strings.Join(got.Project.EnvFiles, ",") != "custom.env" {
		t.Fatal(got)
	}
	rev, e := revision(got.Project)
	if e != nil {
		t.Fatal(e)
	}
	if e = reloaded.begin(saved.ID, "save", rev, string(before)); e != nil {
		t.Fatal(e)
	}
	reloaded.wg.Wait()
	if len(x.calls) != 1 || x.calls[0] != "config --quiet" {
		t.Fatal(x.calls)
	}
	if e = reloaded.unregisterProject(saved.ID); e != nil {
		t.Fatal(e)
	}
	clean, e := newManager(c, x)
	if e != nil || len(clean.snapshot()) != 0 {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(c.StateDir, saved.ID+".json")); e != nil {
		t.Fatal("history lost", e)
	}
}
func TestRegistrationRejectsRootsMissingMountsAndSymlinks(t *testing.T) {
	for _, tc := range []string{"outside root", "prefix sibling", "missing mount", "relative", "directory symlink", "compose symlink", "env symlink", "implicit env symlink", "traversal", "missing file", "duplicate file", "no roots"} {
		t.Run(tc, func(t *testing.T) {
			c, p := adoptionFixture(t)
			external := canonicalTemp(t)
			switch tc {
			case "outside root":
				p.Directory = external
			case "prefix sibling":
				p.Directory = c.AllowedProjectRoots[0] + "-sibling"
			case "missing mount":
				p.Directory = filepath.Join(c.AllowedProjectRoots[0], "not-mounted")
			case "relative":
				p.Directory = "relative"
			case "directory symlink":
				link := filepath.Join(c.AllowedProjectRoots[0], "link")
				os.Symlink(external, link)
				p.Directory = link
			case "compose symlink":
				os.Remove(filepath.Join(p.Directory, "compose.yaml"))
				os.Symlink(filepath.Join(external, "secret"), filepath.Join(p.Directory, "compose.yaml"))
				os.WriteFile(filepath.Join(external, "secret"), []byte("secret"), 0600)
			case "env symlink":
				os.Remove(filepath.Join(p.Directory, "custom.env"))
				os.Symlink(filepath.Join(p.Directory, "override.yaml"), filepath.Join(p.Directory, "custom.env"))
			case "implicit env symlink":
				os.Symlink(filepath.Join(p.Directory, "override.yaml"), filepath.Join(p.Directory, ".env"))
			case "traversal":
				p.ComposeFiles = []string{"../compose.yaml"}
			case "missing file":
				p.ComposeFiles = []string{"absent.yaml"}
			case "duplicate file":
				p.ComposeFiles = []string{"compose.yaml", "compose.yaml"}
			case "no roots":
				c.AllowedProjectRoots = []string{}
			}
			m, e := newManager(c, &fakeExecutor{})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = m.registerProject(p); e == nil {
				t.Fatal("unsafe registration accepted")
			}
			if len(m.snapshot()) != 0 {
				t.Fatal("partially adopted")
			}
			if _, e = os.Stat(filepath.Join(c.StateDir, "registered-projects.json")); !os.IsNotExist(e) {
				t.Fatal("registry written on rejection")
			}
		})
	}
	if len(allowedRoots(Config{Projects: []Project{{Directory: "/"}}})) != 0 {
		t.Fatal("filesystem root inherited as a UI authorization root")
	}
	if validateRoots([]string{"/"}) == nil {
		t.Fatal("host root accepted")
	}
}
func TestRegistrationConflictsConcurrencyAndPersistenceFailure(t *testing.T) {
	c, p := adoptionFixture(t)
	m, e := newManager(c, &fakeExecutor{})
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := m.registerProject(p); e == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
			_ = m.snapshot()
			_, _ = m.get("unknown")
		}()
	}
	wg.Wait()
	if success != 1 || len(m.snapshot()) != 1 {
		t.Fatal(success)
	}
	p.ProjectName = "other-app"
	if _, e = m.registerProject(p); e == nil || !strings.Contains(e.Error(), "shared") {
		t.Fatal("shared file accepted", e)
	}
	// A failure must not publish a new in-memory project; external edits must not be overwritten.
	c2, p2 := adoptionFixture(t)
	m2, _ := newManager(c2, &fakeExecutor{})
	os.WriteFile(filepath.Join(c2.StateDir, "registered-projects.json"), []byte("external edit"), 0600)
	if _, e = m2.registerProject(p2); e == nil || len(m2.snapshot()) != 0 {
		t.Fatal("persistence failure did not abort registration")
	}
	if _, e = newManager(c2, &fakeExecutor{}); e == nil {
		t.Fatal("corrupt registry silently discarded")
	}
	c3, p3 := adoptionFixture(t)
	m3, _ := newManager(c3, &fakeExecutor{})
	if e = os.Remove(c3.StateDir); e != nil {
		t.Fatal(e)
	}
	if _, e = m3.registerProject(p3); e == nil || len(m3.snapshot()) != 0 {
		t.Fatal("missing state dir registration accepted")
	}
}
func TestRegistrationAuthConfirmationUntrustedHintsAndSelfProtection(t *testing.T) {
	c, p := adoptionFixture(t)
	x := &fakeExecutor{}
	m, _ := newManager(c, x)
	s := newServer(m, c, "long-password-123456")
	s.discovery = &inventoryStub{rows: []docker.Container{{ID: "other", ComposeProject: p.ProjectName, WorkingDir: "/unmounted/<script>alert(1)</script>", ConfigFiles: "/outside/a.yaml"}, {ID: "reef", ComposeProject: "reef-self", Reef: true}}}
	h := s.Handler()
	s.sessions[sessionKey("test")] = session{CSRF: "token", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "test"}
	body := url.Values{"csrf": {"token"}, "confirmed": {"yes"}, "project_name": {p.ProjectName}, "name": {p.Name}, "directory": {p.Directory}, "compose_files": {strings.Join(p.ComposeFiles, "\n")}, "env_files": {strings.Join(p.EnvFiles, "\n")}}
	for _, path := range []string{"/api/projects/proposal?name=existing-app", "/api/projects/register", "/api/projects/unmanage"} {
		method := "POST"
		if strings.Contains(path, "proposal") {
			method = "GET"
		}
		if w := call(h, method, path, body.Encode(), "http://reef.test", nil); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	if w := call(h, "POST", "/api/projects/register", body.Encode(), "http://evil.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	bad := body.Encode()
	body.Set("csrf", "bad")
	if w := call(h, "POST", "/api/projects/register", body.Encode(), "http://reef.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	body.Set("csrf", "token")
	body.Del("confirmed")
	if w := call(h, "POST", "/api/projects/register", body.Encode(), "http://reef.test", cookie); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := call(h, "GET", "/api/projects/proposal?name=existing-app", "", "", cookie)
	if w.Code != 200 || len(m.snapshot()) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	var hint struct {
		Directory string `json:"directory"`
	}
	json.Unmarshal(w.Body.Bytes(), &hint)
	if !strings.Contains(hint.Directory, "<script>") {
		t.Fatal("hint not preserved as data")
	}
	if w = call(h, "GET", "/api/projects/proposal?name=reef-self", "", "", cookie); w.Code != 409 {
		t.Fatal("self adoption allowed")
	}
	body.Set("confirmed", "yes")
	body.Set("project_name", "reef-self")
	if w = call(h, "POST", "/api/projects/register", body.Encode(), "http://reef.test", cookie); w.Code != 409 {
		t.Fatal("self adoption allowed")
	}
	w = call(h, "POST", "/api/projects/register", bad, "http://reef.test", cookie)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(x.calls) != 0 {
		t.Fatal("adoption ran Compose")
	}
	var saved Project
	json.Unmarshal(w.Body.Bytes(), &saved)
	if w = call(h, "GET", "/api/document?id="+saved.ID, "", "", cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = call(h, "POST", "/api/projects/register", bad, "http://reef.test", cookie); w.Code != 409 {
		t.Fatal("duplicate adopted")
	}
	mp, _ := m.get(saved.ID)
	mp.busy = true
	unmanage := url.Values{"csrf": {"token"}, "confirmed": {"yes"}, "id": {saved.ID}}.Encode()
	if w = call(h, "POST", "/api/projects/unmanage", unmanage, "http://reef.test", cookie); w.Code != 409 {
		t.Fatal("busy project removed")
	}
	mp.busy = false
	if w = call(h, "POST", "/api/projects/unmanage", unmanage, "http://reef.test", cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestExistingConfigRemainsAvailableAndRootRevocationFailsClosed(t *testing.T) {
	c, p := adoptionFixture(t)
	existing := p
	existing.ID = "configured"
	existing.ProjectName = "configured-project"
	existing.ComposeFiles = []string{"override.yaml"}
	existing.EnvFiles = nil
	c.Projects = []Project{existing}
	p.ComposeFiles = []string{"compose.yaml"}
	m, e := newManager(c, &fakeExecutor{})
	if e != nil {
		t.Fatal(e)
	}
	saved, e := m.registerProject(p)
	if e != nil {
		t.Fatal(e)
	}
	again, e := newManager(c, &fakeExecutor{})
	if e != nil || len(again.snapshot()) != 2 {
		t.Fatal(e)
	}
	if e = again.unregisterProject(existing.ID); e == nil {
		t.Fatal("configured project removed")
	}
	c.AllowedProjectRoots = []string{}
	if _, e = newManager(c, &fakeExecutor{}); e == nil {
		t.Fatal("revoked root reloaded")
	}
	if saved.ProjectName != p.ProjectName {
		t.Fatal("identity changed")
	}
}

func TestRegistrationErrorsIdentifyTheFieldWithoutAuthorizingFiles(t *testing.T) {
	c, p := adoptionFixture(t)
	m, _ := newManager(c, &fakeExecutor{})
	s := newServer(m, c, "long-password-123456")
	s.discovery = &inventoryStub{rows: []docker.Container{{ID: "fixture", ComposeProject: p.ProjectName}}}
	s.sessions[sessionKey("field-test")] = session{CSRF: "token", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "field-test"}
	body := url.Values{"csrf": {"token"}, "confirmed": {"yes"}, "project_name": {p.ProjectName}, "name": {p.Name}, "directory": {p.Directory}, "compose_files": {"compose.yaml"}}
	for _, tc := range []struct{ key, value, field string }{{"directory", filepath.Join(c.AllowedProjectRoots[0], "missing"), "directory"}, {"compose_files", "missing.yaml", "compose_files"}, {"env_files", "missing.env", "env_files"}} {
		testBody := url.Values{}
		for k, v := range body {
			testBody[k] = append([]string{}, v...)
		}
		testBody.Set(tc.key, tc.value)
		w := call(s.Handler(), "POST", "/api/projects/register", testBody.Encode(), "http://reef.test", cookie)
		if w.Code != 409 || w.Header().Get("X-Reef-Field") != tc.field {
			t.Fatal(w.Code, w.Header(), w.Body.String())
		}
	}
	if len(m.snapshot()) != 0 {
		t.Fatal("validation error partially registered a project")
	}
}

func TestProjectDirectoryAliasResolutionRequiresAuthorizedPhysicalDestination(t *testing.T) {
	c, p := adoptionFixture(t)
	alias := filepath.Join(canonicalTemp(t), "share-alias")
	if e := os.Symlink(c.AllowedProjectRoots[0], alias); e != nil {
		t.Fatal(e)
	}
	candidate := filepath.Join(alias, "app")
	physical, e := resolveProjectDirectory(c, candidate)
	if e != nil || physical != p.Directory {
		t.Fatal(physical, e)
	}
	for _, tc := range []struct {
		name, dir string
		cfg       Config
	}{
		{"outside", canonicalTemp(t), c},
		{"missing alias", filepath.Join(alias, "absent"), c},
		{"relative", "app", c},
		{"traversal", alias + "/../app", c},
		{"explicit deny", candidate, Config{AllowedProjectRoots: []string{}}},
		{"installation", candidate, Config{AllowedProjectRoots: c.AllowedProjectRoots, InstallationDirectory: p.Directory}},
		{"file", filepath.Join(candidate, "compose.yaml"), c},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, e := resolveProjectDirectory(tc.cfg, tc.dir); e == nil || got != "" {
				t.Fatal("unauthorized resolution disclosed", got, e)
			}
		})
	}
	// Resolving a preview does not authorize a later direct alias submission.
	m, _ := newManager(c, &fakeExecutor{})
	p.Directory = candidate
	if _, e = m.registerProject(p); e == nil {
		t.Fatal("unreviewed alias registered")
	}
	p.Directory = physical
	saved, e := m.registerProject(p)
	if e != nil || saved.Directory != physical {
		t.Fatal(saved, e)
	}
	reloaded, e := newManager(c, &fakeExecutor{})
	if e != nil {
		t.Fatal(e)
	}
	if got, ok := reloaded.get(saved.ID); !ok || got.Project.Directory != physical {
		t.Fatal("canonical path not persisted")
	}
}

func TestProjectProposalResolvesAliasWithoutRegisteringOrReadingCompose(t *testing.T) {
	c, p := adoptionFixture(t)
	alias := filepath.Join(canonicalTemp(t), "nas-share")
	os.Symlink(c.AllowedProjectRoots[0], alias)
	input := filepath.Join(alias, "app")
	x := &fakeExecutor{}
	m, _ := newManager(c, x)
	s := newServer(m, c, "long-password-123456")
	// Both alias-based and physical absolute file labels become relative paths.
	s.discovery = &inventoryStub{rows: []docker.Container{{ComposeProject: p.ProjectName, WorkingDir: input, ConfigFiles: filepath.Join(input, "compose.yaml") + "," + filepath.Join(p.Directory, "override.yaml")}}}
	s.sessions[sessionKey("preview")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "preview"}
	route := "/api/projects/proposal?name=" + p.ProjectName
	response := call(s.Handler(), "GET", route, "", "", cookie)
	var data struct {
		Directory string   `json:"directory"`
		Resolved  string   `json:"resolved_directory"`
		Files     []string `json:"compose_files"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &data) != nil || data.Resolved != p.Directory || data.Directory != input || strings.Join(data.Files, ",") != "compose.yaml,override.yaml" {
		t.Fatal(response.Code, response.Body.String())
	}
	// Preview needs no Compose file contents or syntax, and never runs Compose.
	os.Remove(filepath.Join(p.Directory, "compose.yaml"))
	response = call(s.Handler(), "GET", route+"&directory="+url.QueryEscape(input), "", "", cookie)
	if response.Code != 200 || len(x.calls) != 0 || len(m.snapshot()) != 0 {
		t.Fatal(response.Code, response.Body.String())
	}
	outside := canonicalTemp(t)
	os.Remove(alias)
	os.Symlink(outside, alias)
	response = call(s.Handler(), "GET", route+"&directory="+url.QueryEscape(alias), "", "", cookie)
	if response.Code != 409 || response.Header().Get("X-Reef-Field") != "directory" || strings.Contains(response.Body.String(), outside) {
		t.Fatal("unsafe alias exposed", response.Code, response.Body.String())
	}
}

func TestUnavailableHostAliasUsesOnlyExplicitRootMapping(t *testing.T) {
	c, p := adoptionFixture(t)
	alias := filepath.Join(canonicalTemp(t), "not-mounted-share")
	c.ProjectPathAliases = map[string]string{alias: c.AllowedProjectRoots[0]}
	input := filepath.Join(alias, "app")
	if _, e := os.Lstat(alias); !os.IsNotExist(e) {
		t.Fatal("fixture alias exists")
	}
	physical, e := resolveProjectDirectory(c, input)
	if e != nil || physical != p.Directory {
		t.Fatal(physical, e)
	}
	if _, e = os.Lstat(alias); !os.IsNotExist(e) {
		t.Fatal("resolution created host alias")
	}
	// No suffix guessing, sibling-prefix matching, traversal or target escape.
	for _, bad := range []string{alias + "-sibling/app", alias + "/../app", filepath.Join(alias, "missing")} {
		if got, e := resolveProjectDirectory(c, bad); e == nil || got != "" {
			t.Fatal("unverified alias resolved", got, e)
		}
	}
	os.Symlink(canonicalTemp(t), filepath.Join(c.AllowedProjectRoots[0], "escape"))
	if got, e := resolveProjectDirectory(c, filepath.Join(alias, "escape")); e == nil || got != "" {
		t.Fatal("mapped alias escaped root", got, e)
	}
	// A visible symlink pointing elsewhere must not be overridden by the mapping.
	os.Symlink(canonicalTemp(t), alias)
	if got, e := resolveProjectDirectory(c, alias); e == nil || got != "" {
		t.Fatal("visible alias destination ignored", got, e)
	}
	os.Remove(alias)
	m, _ := newManager(c, &fakeExecutor{})
	s := newServer(m, c, "long-password-123456")
	s.discovery = &inventoryStub{rows: []docker.Container{{ComposeProject: p.ProjectName, WorkingDir: input, ConfigFiles: filepath.Join(input, "compose.yaml")}}}
	s.sessions[sessionKey("mapping")] = session{Expires: time.Now().Add(time.Hour)}
	response := call(s.Handler(), "GET", "/api/projects/proposal?name="+p.ProjectName, "", "", &http.Cookie{Name: "qcu_session", Value: "mapping"})
	var data struct {
		Resolved string   `json:"resolved_directory"`
		Files    []string `json:"compose_files"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &data) != nil || data.Resolved != p.Directory || len(data.Files) != 1 || data.Files[0] != "compose.yaml" {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestProjectPathAliasConfigValidation(t *testing.T) {
	for _, kind := range []string{"valid", "relative", "filesystem root", "unapproved target", "empty roots", "overlapping aliases", "overlapping root", "too many"} {
		t.Run(kind, func(t *testing.T) {
			c, _ := adoptionFixture(t)
			alias := filepath.Join(canonicalTemp(t), "alias")
			target := c.AllowedProjectRoots[0]
			c.ProjectPathAliases = map[string]string{alias: target}
			switch kind {
			case "relative":
				c.ProjectPathAliases = map[string]string{"relative": target}
			case "filesystem root":
				c.ProjectPathAliases = map[string]string{"/": target}
			case "unapproved target":
				c.ProjectPathAliases[alias] = canonicalTemp(t)
			case "empty roots":
				c.AllowedProjectRoots = []string{}
			case "overlapping aliases":
				c.ProjectPathAliases[filepath.Join(alias, "nested")] = target
			case "overlapping root":
				c.ProjectPathAliases = map[string]string{filepath.Dir(target): target}
			case "too many":
				for i := 0; i < 33; i++ {
					c.ProjectPathAliases[filepath.Join(alias, strings.Repeat("a", i+1))] = target
				}
			}
			_, e := finishConfig(c)
			if (e == nil) != (kind == "valid") {
				t.Fatal(kind, e)
			}
		})
	}
}
