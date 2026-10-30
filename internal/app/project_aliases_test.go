package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reef/internal/docker"
	"strings"
	"testing"
	"time"
)

func TestSavedProjectAliasesPersistAndNeverAlterProjectFiles(t *testing.T) {
	c, p := adoptionFixture(t)
	m, e := newManager(c, &fakeExecutor{})
	if e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(canonicalTemp(t), "host-only")
	input := filepath.Join(alias, "app")
	before, _ := os.ReadFile(filepath.Join(p.Directory, "compose.yaml"))
	physical, e := m.savePathAlias("save", alias, c.AllowedProjectRoots[0], input, p)
	if e != nil || physical != p.Directory {
		t.Fatal(physical, e)
	}
	if resolved, e := resolveProjectDirectory(m.aliasConfig(), input); e != nil || resolved != p.Directory {
		t.Fatal(resolved, e)
	}
	info, e := os.Stat(filepath.Join(c.StateDir, "project-path-aliases.json"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, e)
	}
	after, _ := os.ReadFile(filepath.Join(p.Directory, "compose.yaml"))
	if string(before) != string(after) {
		t.Fatal("project files changed")
	}
	reloaded, e := newManager(c, &fakeExecutor{})
	if e != nil || len(reloaded.aliasList()) != 1 || !reloaded.aliasList()[0].Editable {
		t.Fatal("saved alias lost", e)
	}
	p.Directory = physical
	saved, e := reloaded.registerProject(p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = reloaded.savePathAlias("remove", alias, "", "", Project{}); e != nil {
		t.Fatal(e)
	}
	again, e := newManager(c, &fakeExecutor{})
	if e != nil || len(again.aliasList()) != 0 {
		t.Fatal(e)
	}
	if got, ok := again.get(saved.ID); !ok || got.Project.Directory != physical {
		t.Fatal("removing alias changed registration")
	}
}

func TestProjectAliasPersistenceFailsClosed(t *testing.T) {
	for _, mode := range []string{"external edit", "corrupt state", "symlink", "public permissions", "missing target", "missing files", "config alias"} {
		t.Run(mode, func(t *testing.T) {
			c, p := adoptionFixture(t)
			alias := filepath.Join(canonicalTemp(t), "host-only")
			if mode == "config alias" {
				c.ProjectPathAliases = map[string]string{alias: c.AllowedProjectRoots[0]}
			}
			m, e := newManager(c, &fakeExecutor{})
			if e != nil {
				t.Fatal(e)
			}
			state := filepath.Join(c.StateDir, "project-path-aliases.json")
			switch mode {
			case "external edit", "corrupt state":
				os.WriteFile(state, []byte("changed"), 0600)
			case "symlink":
				f := filepath.Join(canonicalTemp(t), "private")
				os.WriteFile(f, []byte("private"), 0600)
				os.Symlink(f, state)
			case "public permissions":
				os.WriteFile(state, []byte(`{"version":1,"aliases":{}}`), 0644)
			case "missing target":
				os.RemoveAll(p.Directory)
			case "missing files":
				os.Remove(filepath.Join(p.Directory, "compose.yaml"))
			}
			if _, e = m.savePathAlias("save", alias, c.AllowedProjectRoots[0], filepath.Join(alias, "app"), p); e == nil {
				t.Fatal("invalid edit accepted")
			}
			if _, ok := m.pathAliases[alias]; ok {
				t.Fatal("failed save activated mapping")
			}
			if mode == "corrupt state" || mode == "symlink" || mode == "public permissions" {
				if _, e = newManager(c, &fakeExecutor{}); e == nil {
					t.Fatal("invalid state ignored on restart")
				}
			}
		})
	}
}

func TestAliasSuggestionsAreBoundedHypothesesWithoutActivation(t *testing.T) {
	root := canonicalTemp(t)
	physicalRoot := filepath.Join(root, "physical", "share", "CACHEDEV1_DATA", "Container", "container-station-data", "application")
	os.MkdirAll(filepath.Join(physicalRoot, "gitlab"), 0700)
	os.WriteFile(filepath.Join(physicalRoot, "gitlab", "compose.yaml"), []byte("services: {}"), 0600)
	input := filepath.Join(root, "host-alias", "share", "Container", "container-station-data", "application", "gitlab")
	c := Config{StateDir: canonicalTemp(t), AllowedProjectRoots: []string{physicalRoot}}
	rows := suggestProjectAliases(c, input, []string{"compose.yaml"})
	if len(rows) != 1 || rows[0].Directory != filepath.Join(physicalRoot, "gitlab") || rows[0].Target != physicalRoot {
		t.Fatal(rows)
	}
	if len(c.ProjectPathAliases) != 0 {
		t.Fatal("suggestion activated a mapping")
	}
	if _, e := resolveProjectDirectory(c, input); e == nil {
		t.Fatal("unconfirmed suggestion resolved")
	}
	if rows := suggestProjectAliases(c, input, []string{"missing.yaml"}); len(rows) != 0 {
		t.Fatal("missing files suggested")
	}
	if rows := suggestProjectAliases(c, filepath.Join(root, "applications", "gitlab"), []string{"compose.yaml"}); len(rows) != 0 {
		t.Fatal("single name guessed")
	}
	second := filepath.Join(root, "second", "share", "VOLUME2", "Container", "container-station-data", "application")
	os.MkdirAll(filepath.Join(second, "gitlab"), 0700)
	os.WriteFile(filepath.Join(second, "gitlab", "compose.yaml"), []byte("services: {}"), 0600)
	c.AllowedProjectRoots = append(c.AllowedProjectRoots, second)
	if rows := suggestProjectAliases(c, input, []string{"compose.yaml"}); len(rows) != 2 {
		t.Fatal("ambiguous choices concealed", rows)
	}
}

func TestProjectAliasAPIRequiresAuthCSRFAndExplicitConfirmation(t *testing.T) {
	c, p := adoptionFixture(t)
	m, _ := newManager(c, &fakeExecutor{})
	s := newServer(m, c, "long-password-123456")
	alias := filepath.Join(canonicalTemp(t), "host-only")
	s.discovery = &inventoryStub{rows: []docker.Container{{ComposeProject: p.ProjectName}}}
	s.sessions[sessionKey("alias-api")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "alias-api"}
	form := url.Values{"csrf": {"csrf"}, "confirmed": {"yes"}, "action": {"save"}, "alias": {alias}, "target": {c.AllowedProjectRoots[0]}, "directory": {filepath.Join(alias, "app")}, "project_name": {p.ProjectName}, "compose_files": {strings.Join(p.ComposeFiles, "\n")}, "env_files": {strings.Join(p.EnvFiles, "\n")}}
	route := "/api/projects/path-aliases"
	if w := call(s.Handler(), "POST", route, form.Encode(), "http://reef.test", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(s.Handler(), "POST", route, form.Encode(), "http://evil.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	form.Set("csrf", "wrong")
	if w := call(s.Handler(), "POST", route, form.Encode(), "http://reef.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	form.Set("csrf", "csrf")
	form.Del("confirmed")
	if w := call(s.Handler(), "POST", route, form.Encode(), "http://reef.test", cookie); w.Code != 400 {
		t.Fatal(w.Code)
	}
	form.Set("confirmed", "yes")
	response := call(s.Handler(), "POST", route, form.Encode(), "http://reef.test", cookie)
	var data struct {
		Directory string `json:"resolved_directory"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &data) != nil || data.Directory != p.Directory {
		t.Fatal(response.Code, response.Body.String())
	}
	if len(m.snapshot()) != 0 {
		t.Fatal("alias save registered a project")
	}
}
