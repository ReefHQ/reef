package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reef/internal/agents"
	"strings"
	"testing"
	"time"
)

const gitlabOld = "gitlab/gitlab-ce:19.5.1-ce.0"
const gitlabNext = "gitlab/gitlab-ce:19.5.3-ce.0"

type imageProposalExecutor struct {
	override, sideEffect bool
	calls                []string
}

func (x *imageProposalExecutor) Run(ctx context.Context, p Project, args []string, w io.Writer) error {
	x.calls = append(x.calls, strings.Join(args, " "))
	ref := gitlabOld
	candidate := false
	for _, arg := range args {
		if strings.Contains(arg, ".reef-image-candidate-") {
			candidate = true
			raw, e := os.ReadFile(arg)
			if e != nil {
				return e
			}
			if strings.Contains(string(raw), gitlabNext) && !x.override {
				ref = gitlabNext
			}
		}
	}
	service := map[string]any{"image": ref, "restart": "unless-stopped"}
	if candidate && x.sideEffect {
		service["restart"] = "always"
	}
	return json.NewEncoder(w).Encode(map[string]any{"services": map[string]any{"web": service}})
}
func imageProposalFixture(t *testing.T) (*Manager, Project, *imageProposalExecutor, string) {
	t.Helper()
	c, p := adoptionFixture(t)
	p.ID = "app"
	c.Projects = []Project{p}
	content := "services:\n  web:\n    image: " + gitlabOld + " # keep comment\n    restart: unless-stopped\n"
	if e := os.WriteFile(filepath.Join(p.Directory, p.ComposeFiles[0]), []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	x := &imageProposalExecutor{}
	m, e := newManager(c, x)
	if e != nil {
		t.Fatal(e)
	}
	m.Images = &ImageService{Exec: x}
	rev, e := revision(p)
	if e != nil {
		t.Fatal(e)
	}
	m.Projects[p.ID].ImageUpdates = ImageUpdates{CheckedAt: time.Now(), Revision: rev, Services: []ImageUpdate{{Service: "web", Image: gitlabOld, Status: "current", VersionStatus: "available", Candidate: gitlabNext}}}
	return m, p, x, content
}
func TestImageVersionProposalChangesOnlyUnsavedServiceImage(t *testing.T) {
	m, p, x, before := imageProposalFixture(t)
	rev, _ := revision(p)
	content, e := m.imageVersionProposal(context.Background(), p.ID, "web", rev, gitlabNext)
	if e != nil || content != strings.Replace(before, gitlabOld, gitlabNext, 1) {
		t.Fatal(content, e)
	}
	raw, _ := os.ReadFile(filepath.Join(p.Directory, p.ComposeFiles[0]))
	if string(raw) != before {
		t.Fatal("source Compose changed")
	}
	files, _ := filepath.Glob(filepath.Join(p.Directory, ".reef-image-candidate-*"))
	if len(files) != 0 {
		t.Fatal("private candidate leaked")
	}
	if m.Projects[p.ID].busy || m.Projects[p.ID].Op.ID != "" {
		t.Fatal("preparation started a mutation")
	}
	if len(x.calls) != 2 {
		t.Fatal(x.calls)
	}
	for _, cmd := range x.calls {
		if !strings.HasSuffix(cmd, "config --format json") {
			t.Fatal("unexpected Docker command", cmd)
		}
	}
}
func TestImageVersionProposalRejectsStaleAmbiguousAndOverriddenEdits(t *testing.T) {
	for _, mode := range []string{"revision", "duplicate", "variable", "override", "side effect", "busy", "no candidate", "changed candidate"} {
		t.Run(mode, func(t *testing.T) {
			m, p, x, before := imageProposalFixture(t)
			rev, _ := revision(p)
			switch mode {
			case "revision":
				rev = "old"
			case "duplicate":
				os.WriteFile(filepath.Join(p.Directory, p.ComposeFiles[0]), []byte(before+"  other:\n    image: "+gitlabOld+"\n"), 0600)
			case "variable":
				os.WriteFile(filepath.Join(p.Directory, p.ComposeFiles[0]), []byte("services:\n  web:\n    image: ${APP_IMAGE}\n"), 0600)
			case "override":
				x.override = true
			case "side effect":
				x.sideEffect = true
			case "busy":
				m.Projects[p.ID].busy = true
			case "changed candidate":
				m.Projects[p.ID].ImageUpdates.Services[0].Candidate = "gitlab/gitlab-ce:19.5.4-ce.0"
			case "no candidate":
				m.Projects[p.ID].ImageUpdates.Services = nil
			}
			if mode == "duplicate" || mode == "variable" {
				rev, _ = revision(p)
				m.Projects[p.ID].ImageUpdates.Revision = rev
			}
			if _, e := m.imageVersionProposal(context.Background(), p.ID, "web", rev, gitlabNext); e == nil {
				t.Fatal("unsafe proposal accepted", mode)
			}
			files, _ := filepath.Glob(filepath.Join(p.Directory, ".reef-image-candidate-*"))
			if len(files) != 0 {
				t.Fatal("candidate leaked")
			}
		})
	}
}
func TestImageVersionPreparationAuthAndRemoteHostIsolation(t *testing.T) {
	remote, p, _, _ := imageProposalFixture(t)
	local, _, _ := fixture(t, &fakeExecutor{})
	central := newServer(local, Config{AgentsEnabled: true}, "long-password-123456")
	central.agents, _ = agents.Open(t.TempDir())
	central.sessions[sessionKey("tag-browser")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	backend := newServer(remote, remote.config, "unused-password")
	host, _ := relayAgentFixture(t, central, backend, "tag-host")
	cookie := &http.Cookie{Name: "qcu_session", Value: "tag-browser"}
	rev, _ := revision(p)
	form := url.Values{"csrf": {"csrf"}, "confirmed": {"yes"}, "id": {p.ID}, "service": {"web"}, "revision": {rev}, "candidate": {gitlabNext}}
	route := "/api/image-updates/prepare?host=" + host
	if w := call(central.Handler(), "POST", route, form.Encode(), "http://reef.test", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(central.Handler(), "POST", route, form.Encode(), "http://evil.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	form.Set("csrf", "wrong")
	if w := call(central.Handler(), "POST", route, form.Encode(), "http://reef.test", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	form.Set("csrf", "csrf")
	form.Del("confirmed")
	if w := call(central.Handler(), "POST", route, form.Encode(), "http://reef.test", cookie); w.Code != 400 {
		t.Fatal(w.Code)
	}
	form.Set("confirmed", "yes")
	response := call(central.Handler(), "POST", route, form.Encode(), "http://reef.test", cookie)
	var doc struct {
		Content string `json:"content"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &doc) != nil || !strings.Contains(doc.Content, gitlabNext) {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestComposeImageVersionProposalIntegration(t *testing.T) {
	if os.Getenv("REEF_TEST_IMAGE_PROPOSAL") != "1" {
		t.Skip("opt-in Compose metadata-only integration, no engine socket required")
	}
	for _, mode := range []string{"literal", "anchor", "override", "variable"} {
		t.Run(mode, func(t *testing.T) {
			c, p := adoptionFixture(t)
			p.ID = "app"
			p.EnvFiles = nil
			p.ComposeFiles = []string{"compose.yaml"}
			c.Projects = []Project{p}
			content := "services:\n  web:\n    image: " + gitlabOld + "\n"
			switch mode {
			case "anchor":
				content = "x-base: &base\n  image: " + gitlabOld + "\nservices:\n  web:\n    <<: *base\n  other:\n    <<: *base\n"
			case "override":
				p.ComposeFiles = append(p.ComposeFiles, "override.yaml")
				os.WriteFile(filepath.Join(p.Directory, "override.yaml"), []byte(content), 0600)
				c.Projects = []Project{p}
			case "variable":
				content = "services:\n  web:\n    image: ${APP_IMAGE}\n"
				os.WriteFile(filepath.Join(p.Directory, ".env"), []byte("APP_IMAGE="+gitlabOld+"\n"), 0600)
			}
			os.WriteFile(filepath.Join(p.Directory, "compose.yaml"), []byte(content), 0600)
			executor := DockerExecutor{Socket: filepath.Join(p.Directory, "NO_ENGINE.sock")}
			m, e := newManager(c, executor)
			if e != nil {
				t.Fatal(e)
			}
			m.Images = &ImageService{Exec: executor}
			rev, e := revision(p)
			if e != nil {
				t.Fatal(e)
			}
			m.Projects[p.ID].ImageUpdates = ImageUpdates{Revision: rev, Services: []ImageUpdate{{Service: "web", Image: gitlabOld, Candidate: gitlabNext, VersionStatus: "available"}}}
			proposed, e := m.imageVersionProposal(context.Background(), p.ID, "web", rev, gitlabNext)
			if (e == nil) != (mode == "literal") {
				t.Fatal(mode, e)
			}
			if e == nil && !strings.Contains(proposed, gitlabNext) {
				t.Fatal("tag not prepared")
			}
			disk, _ := os.ReadFile(filepath.Join(p.Directory, "compose.yaml"))
			if string(disk) != content {
				t.Fatal("Compose source changed")
			}
			candidates, _ := filepath.Glob(filepath.Join(p.Directory, ".reef-image-candidate-*"))
			if len(candidates) != 0 {
				t.Fatal("candidate leaked")
			}
		})
	}
}
