package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reef/internal/agents"
	"strings"
	"sync"
	"testing"
	"time"
)

type containerRoundTrip func(*http.Request) (*http.Response, error)

func (f containerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type containerEngineFixture struct {
	mu                sync.Mutex
	items             map[string]inspectedContainer
	calls             []string
	fail              string
	replacementHealth string
	plan              map[string]json.RawMessage
	pulls             int
	onPull            func()
	onCreate          func()
}

const standaloneOldID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const standaloneNewID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func standaloneFixture(t *testing.T) (*Server, *containerEngineFixture) {
	t.Helper()
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{StateDir: m.StateDir}, "long-password-123456")
	var old inspectedContainer
	json.Unmarshal([]byte(`{"Id":"`+standaloneOldID+`","Name":"/standalone","Image":"sha256:`+strings.Repeat("1", 64)+`","Config":{"Image":"example/app:latest","Env":["SECRET=never-expose-123"],"Cmd":["serve"],"Labels":{"user.label":"kept"}},"HostConfig":{"RestartPolicy":{"Name":"unless-stopped","MaximumRetryCount":0},"NetworkMode":"application","PortBindings":{"80/tcp":[{"HostIp":"127.0.0.1","HostPort":"18080"}]},"Memory":12345,"Binds":["/data"]},"Mounts":[{"Type":"volume","Name":"anonymous-existing","Source":"/private/host-path","Destination":"/data","RW":true}],"State":{"Status":"exited","Running":false},"NetworkSettings":{"Networks":{"application":{"NetworkID":"network","Aliases":["standalone","custom-alias"],"IPAMConfig":{"IPv4Address":"172.30.0.5"}}}}}`), &old)
	f := &containerEngineFixture{items: map[string]inspectedContainer{old.ID: old}}
	s.m.Exec = f
	s.m.containers.api = &containerAPI{http: &http.Client{Transport: containerRoundTrip(f.roundTrip)}}
	return s, f
}
func (f *containerEngineFixture) Run(_ context.Context, _ Project, args []string, _ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Join(args, " ") != "image pull example/app:latest" {
		return errors.New("unexpected CLI command")
	}
	f.pulls++
	if f.onPull != nil {
		f.onPull()
	}
	if f.fail == "pull" {
		return errors.New("SECRET raw pull failure")
	}
	return nil
}
func (f *containerEngineFixture) roundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	out := any(map[string]any{})
	status := 200
	if f.fail == r.Method+" "+path {
		return &http.Response{StatusCode: 500, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"message":"SECRET engine /private/path"}`))}, nil
	}
	switch {
	case path == "/containers/json":
		rows := []map[string]string{}
		for id := range f.items {
			rows = append(rows, map[string]string{"Id": id})
		}
		out = rows
	case strings.HasPrefix(path, "/images/"):
		out = map[string]any{"Id": "sha256:" + strings.Repeat("2", 64), "Os": "linux", "Architecture": "amd64"}
	case strings.HasPrefix(path, "/networks/") && r.Method == "GET":
		out = map[string]any{"Driver": "bridge", "Scope": "local"}
	case strings.HasPrefix(path, "/networks/"):
		var b struct {
			Container      string
			EndpointConfig map[string]json.RawMessage
		}
		json.NewDecoder(r.Body).Decode(&b)
		v, ok := f.items[b.Container]
		if !ok {
			status = 404
			break
		}
		name := strings.Split(path, "/")[2]
		if strings.HasSuffix(path, "/disconnect") {
			delete(v.NetworkSettings.Networks, name)
		} else {
			v.NetworkSettings.Networks[name] = b.EndpointConfig
		}
		f.items[b.Container] = v
	case path == "/containers/create":
		json.NewDecoder(r.Body).Decode(&f.plan)
		v := inspectedContainer{ID: standaloneNewID, Name: "/" + r.URL.Query().Get("name"), Image: value[string](f.plan, "Image"), Config: map[string]json.RawMessage{}, HostConfig: value[map[string]json.RawMessage](f.plan, "HostConfig")}
		for k, b := range f.plan {
			if k != "HostConfig" && k != "NetworkingConfig" {
				v.Config[k] = b
			}
		}
		v.State.Status = "created"
		cfg := value[struct {
			EndpointsConfig map[string]map[string]json.RawMessage
		}](f.plan, "NetworkingConfig")
		v.NetworkSettings.Networks = cfg.EndpointsConfig
		f.items[v.ID] = v
		if f.onCreate != nil {
			f.onCreate()
		}
		out = map[string]string{"Id": v.ID}
		status = 201
	case strings.HasPrefix(path, "/containers/"):
		parts := strings.Split(path, "/")
		id := parts[2]
		v, ok := f.items[id]
		if !ok {
			status = 404
			break
		}
		action := ""
		if len(parts) > 3 {
			action = parts[3]
		}
		if r.Method == "GET" {
			out = v
			break
		}
		if r.Method == "DELETE" {
			if r.URL.Query().Get("v") != "false" || r.URL.Query().Get("force") != "false" {
				panic("Unsafe delete")
			}
			delete(f.items, id)
			break
		}
		switch action {
		case "start", "restart":
			v.State.Running = true
			v.State.Status = "running"
			if id == standaloneNewID && f.replacementHealth != "" {
				v.State.Health = &struct{ Status string }{f.replacementHealth}
			}
		case "stop":
			v.State.Running = false
			v.State.Status = "exited"
		case "rename":
			v.Name = "/" + r.URL.Query().Get("name")
		case "update":
			var body map[string]json.RawMessage
			json.NewDecoder(r.Body).Decode(&body)
			v.HostConfig["RestartPolicy"] = body["RestartPolicy"]
		}
		f.items[id] = v
	default:
		status = 404
	}
	b, _ := json.Marshal(out)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
}
func runStandalone(t *testing.T, s *Server, action string) containerOperation {
	t.Helper()
	r, _, e := s.m.containers.review(context.Background(), standaloneOldID)
	if e != nil {
		t.Fatal(e)
	}
	op, e := s.m.beginContainer(action, standaloneOldID, r.Fingerprint)
	if e != nil {
		t.Fatal(e)
	}
	s.m.wg.Wait()
	got, e := s.m.containers.snapshot()
	if e != nil {
		t.Fatal(e)
	}
	if got.ID != op.ID {
		t.Fatal("Operation changed")
	}
	return got
}
func TestStandaloneUpdatePreservesPrivateConfigurationAndExistingAnonymousVolume(t *testing.T) {
	s, f := standaloneFixture(t)
	op := runStandalone(t, s, "update")
	if !op.OK || op.NewID != standaloneNewID {
		t.Fatal(op)
	}
	if _, ok := f.items[standaloneOldID]; ok {
		t.Fatal("Old container was not retired")
	}
	v := f.items[standaloneNewID]
	if v.Name != "/standalone" || v.State.Running || v.Image != "sha256:"+strings.Repeat("2", 64) {
		t.Fatal("Stopped state/name/image not preserved")
	}
	if value[[]string](f.plan, "Env")[0] != "SECRET=never-expose-123" || value[[]string](f.plan, "Cmd")[0] != "serve" || value[int](v.HostConfig, "Memory") != 12345 || value[struct{ Name string }](v.HostConfig, "RestartPolicy").Name != "unless-stopped" {
		t.Fatal("Configuration changed")
	}
	if got := value[[]string](v.HostConfig, "Binds"); len(got) != 1 || got[0] != "anonymous-existing:/data:rw" {
		t.Fatal("Anonymous volume would be replaced", got)
	}
	review, _, e := s.m.containers.review(context.Background(), standaloneNewID)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal([]any{review, op})
	if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "/private") {
		t.Fatal("Private configuration leaked")
	}
	journal, _ := os.ReadFile(s.m.containers.path)
	if strings.Contains(string(journal), "SECRET") || strings.Contains(string(journal), "/private") {
		t.Fatal("Journal leaked private configuration")
	}
}
func TestStandaloneFailedReplacementRestoresOriginal(t *testing.T) {
	s, f := standaloneFixture(t)
	old := f.items[standaloneOldID]
	old.State.Running = true
	old.State.Status = "running"
	f.items[old.ID] = old
	f.replacementHealth = "unhealthy"
	op := runStandalone(t, s, "update")
	if op.OK || op.Unknown || op.Phase != "rolled_back" {
		t.Fatal(op)
	}
	v := f.items[old.ID]
	if v.Name != old.Name || !v.State.Running || value[struct{ Name string }](v.HostConfig, "RestartPolicy").Name != "unless-stopped" || v.NetworkSettings.Networks["application"] == nil {
		t.Fatal("Original not restored")
	}
	if _, ok := f.items[standaloneNewID]; ok {
		t.Fatal("Failed replacement retained after successful rollback")
	}
}
func TestStandaloneRejectionAndPullFailureDoNotStopOriginal(t *testing.T) {
	for _, mode := range []string{"pull", "changed", "compose", "auto-remove", "shared"} {
		t.Run(mode, func(t *testing.T) {
			s, f := standaloneFixture(t)
			old := f.items[standaloneOldID]
			switch mode {
			case "pull":
				f.fail = "pull"
			case "compose":
				put(old.Config, "Labels", map[string]string{"com.docker.compose.project": "app"})
				f.items[old.ID] = old
			case "auto-remove":
				put(old.HostConfig, "AutoRemove", true)
				f.items[old.ID] = old
			case "changed":
				f.onPull = func() { v := f.items[old.ID]; v.Name = "/changed"; f.items[old.ID] = v }
			case "shared":
				other := old
				other.ID = standaloneNewID
				other.HostConfig = map[string]json.RawMessage{}
				put(other.HostConfig, "NetworkMode", "container:"+old.ID)
				f.items[other.ID] = other
			}
			op := runStandalone(t, s, "update")
			if op.OK || op.Unknown {
				t.Fatal(op)
			}
			for _, call := range f.calls {
				if strings.HasPrefix(call, "POST ") || strings.HasPrefix(call, "DELETE ") {
					t.Fatal("Rejected operation mutated Docker", call)
				}
			}
		})
	}
}
func TestStandaloneLifecycleDeleteRetainsVolumesAndSerializesStorage(t *testing.T) {
	s, f := standaloneFixture(t)
	for _, action := range []string{"start", "restart", "stop", "remove"} {
		op := runStandalone(t, s, action)
		if !op.OK {
			t.Fatal(action, op)
		}
	}
	if len(f.items) != 0 {
		t.Fatal("Container not deleted")
	}
	s, _ = standaloneFixture(t)
	s.m.storageBusy = true
	if _, e := s.m.beginContainer("start", standaloneOldID, strings.Repeat("f", 64)); e == nil {
		t.Fatal("Concurrent storage mutation allowed")
	}
}
func TestStandaloneRestartQuarantinesInterruptedWorkWithoutReplay(t *testing.T) {
	s, f := standaloneFixture(t)
	c := s.m.containers
	c.op = containerOperation{ID: randomID(), ContainerID: standaloneOldID, Running: true, Action: "update", Phase: "starting_replacement", Started: time.Now()}
	if e := c.persist(); e != nil {
		t.Fatal(e)
	}
	restored := openContainerManagement(s.m.StateDir, "")
	op, e := restored.snapshot()
	if e != nil || op.Running || !op.Unknown || op.Phase != "review_required" || len(f.calls) > 0 {
		t.Fatal(op, e)
	}
	s.m.containers = restored
	if _, e = s.m.beginContainer("start", standaloneOldID, strings.Repeat("f", 64)); e == nil {
		t.Fatal("Unknown operation replay permitted")
	}
}
func TestStandalonePermissionsConfirmationAndOldAgentGate(t *testing.T) {
	s, _, cookie, csrf := userServer(t)
	s.m.containers.api = standaloneFixtureAPI(t)
	for _, tc := range []struct {
		cookie       *http.Cookie
		origin, body string
		want         int
	}{{nil, "http://reef.test", "", 401}, {cookie, "http://evil.test", "csrf=" + csrf, 403}, {cookie, "http://reef.test", "csrf=wrong", 403}, {cookie, "http://reef.test", "csrf=" + csrf, 400}} {
		w := call(s.Handler(), "POST", "/api/container-management/action", tc.body, tc.origin, tc.cookie)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	s.cfg.AgentsEnabled = true
	token, _ := s.agents.Token("old")
	host, key, _ := s.agents.Enroll(token, 1)
	gen, _ := s.agents.Begin(host, key, "0.21.0", 1)
	s.agents.Control(host, gen, true)
	w := call(s.Handler(), "GET", "/api/container-management?id="+standaloneOldID+"&host="+host, "", "", cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Upgrade") {
		t.Fatal(w.Code, w.Body.String())
	}
	if !agents.AllowedCommand("GET", "/api/container-management") || !agents.AllowedCommand("POST", "/api/container-management/action") || agents.AllowedCommand("POST", "/containers/create") {
		t.Fatal("Relay allowlist changed incorrectly")
	}
}
func standaloneFixtureAPI(t *testing.T) *containerAPI {
	_, f := standaloneFixture(t)
	return &containerAPI{http: &http.Client{Transport: containerRoundTrip(f.roundTrip)}}
}
func TestStandaloneReviewRejectsStaleIdentityAndStorageFailureBeforeDocker(t *testing.T) {
	s, f := standaloneFixture(t)
	if _, e := s.m.beginContainer("update", standaloneOldID, "invalid"); e == nil {
		t.Fatal("Invalid fingerprint allowed")
	}
	op, e := s.m.beginContainer("update", standaloneOldID, strings.Repeat("f", 64))
	if e != nil {
		t.Fatal(e)
	}
	s.m.wg.Wait()
	op, _ = s.m.containers.snapshot()
	if op.OK || f.pulls > 0 {
		t.Fatal("Stale review pulled or mutated")
	}
	if e = os.Remove(s.m.containers.path); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(s.m.containers.path, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = s.m.beginContainer("start", standaloneOldID, strings.Repeat("f", 64)); e == nil {
		t.Fatal("Unsafe journal accepted")
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "POST") {
			t.Fatal("Storage failure performed mutation")
		}
	}
}

func TestStandaloneRemoteActionsStayOnTheSelectedAgent(t *testing.T) {
	central, local := standaloneFixture(t)
	remote, remoteEngine := standaloneFixture(t)
	central.cfg.AgentsEnabled = true
	central.agents, _ = agents.Open(t.TempDir())
	remote.discovery = &inventoryStub{}
	host, _ := relayAgentFixture(t, central, remote, "standalone-agent", agents.ContainersCapability)
	central.sessions[sessionKey("central-browser")] = session{CSRF: "central-csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "central-browser"}
	w := call(central.Handler(), "GET", "/api/container-management?host="+host+"&id="+standaloneOldID, "", "", cookie)
	var body struct{ Container containerReview }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	form := "csrf=central-csrf&confirmed=yes&action=start&id=" + standaloneOldID + "&fingerprint=" + body.Container.Fingerprint
	w = call(central.Handler(), "POST", "/api/container-management/action?host="+host, form, "http://reef.test", cookie)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	remote.m.wg.Wait()
	if !remoteEngine.items[standaloneOldID].State.Running || local.items[standaloneOldID].State.Running || len(local.calls) > 0 {
		t.Fatal("Remote mutation crossed host boundary")
	}
	if w = call(central.Handler(), "GET", "/api/container-management/operation?host="+host, "", "", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestStandaloneRollbackFailureRequiresReviewAndBlocksOtherMutations(t *testing.T) {
	s, f := standaloneFixture(t)
	old := f.items[standaloneOldID]
	old.State.Running = true
	old.State.Status = "running"
	f.items[old.ID] = old
	f.replacementHealth = "unhealthy"
	f.fail = "DELETE /containers/" + standaloneNewID
	op := runStandalone(t, s, "update")
	if !op.Unknown || op.Running || op.OK || !s.m.storageBusy {
		t.Fatal("Uncertain state was not quarantined", op)
	}
	if _, e := s.m.beginContainer("start", standaloneOldID, strings.Repeat("f", 64)); e == nil {
		t.Fatal("Mutation admitted before review")
	}
	s.sessions[sessionKey("browser")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "browser"}
	w := call(s.Handler(), "POST", "/api/container-management/acknowledge", "csrf=csrf&confirmed=yes&operation="+op.ID, "http://reef.test", cookie)
	if w.Code != 200 || s.m.storageBusy {
		t.Fatal("Acknowledgement did not clear only the block", w.Code)
	}
	if len(f.items) != 2 {
		t.Fatal("Acknowledgement unexpectedly changed Docker")
	}
}

func TestStandaloneChangeAfterPreparationNeverRestoresOverExternalChanges(t *testing.T) {
	s, f := standaloneFixture(t)
	f.onCreate = func() {
		v := f.items[standaloneOldID]
		v.Name = "/externally-renamed"
		put(v.HostConfig, "RestartPolicy", map[string]any{"Name": "no", "MaximumRetryCount": 0})
		f.items[v.ID] = v
	}
	op := runStandalone(t, s, "update")
	if op.OK || op.Unknown || op.Phase != "rolled_back" {
		t.Fatal(op)
	}
	if len(f.items) != 1 || f.items[standaloneOldID].Name != "/externally-renamed" || value[struct{ Name string }](f.items[standaloneOldID].HostConfig, "RestartPolicy").Name != "no" {
		t.Fatal("External configuration was overwritten")
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "POST /containers/"+standaloneOldID) {
			t.Fatal("Original mutated after stale preparation", call)
		}
	}
}
