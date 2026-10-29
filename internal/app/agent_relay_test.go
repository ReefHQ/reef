package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reef/internal/agents"
	"reef/internal/docker"
	"strings"
	"sync"
	"testing"
	"time"
)

type relayTestWriter struct {
	ctx           context.Context
	relay         *agents.Relay
	host, gen, id string
	header        http.Header
	sequence      uint64
	status        int
}

func (w *relayTestWriter) Header() http.Header { return w.header }
func (w *relayTestWriter) send(frame agents.Frame) error {
	w.sequence++
	frame.ID = w.id
	frame.Sequence = w.sequence
	return w.relay.Results(w.host, w.gen, []agents.Frame{frame})
}
func (w *relayTestWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	_ = w.send(agents.Frame{Status: status, ContentType: w.header.Get("Content-Type")})
}
func (w *relayTestWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n := len(data)
	for len(data) > 0 {
		size := len(data)
		if size > agents.MaxFrameBytes {
			size = agents.MaxFrameBytes
		}
		if err := w.send(agents.Frame{Data: append([]byte(nil), data[:size]...)}); err != nil {
			return 0, err
		}
		data = data[size:]
	}
	return n, nil
}
func (w *relayTestWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
}
func (w *relayTestWriter) SetWriteDeadline(time.Time) error { return nil }
func relayAgentFixture(t *testing.T, central *Server, backend *Server, name string, capabilities ...string) (string, context.CancelFunc) {
	t.Helper()
	token, err := central.agents.Token(name)
	if err != nil {
		t.Fatal(err)
	}
	id, key, err := central.agents.Enroll(token, agents.Protocol)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := central.agents.Begin(id, key, "test", agents.Protocol)
	if err != nil {
		t.Fatal(err)
	}
	central.agents.Control(id, gen, true)
	for _, capability := range capabilities {
		if capability == agents.UpdatesCapability {
			central.agents.Updates(id, gen, true)
		}
		if capability == agents.BuildsCapability {
			central.agents.Builds(id, gen, true)
		}
		if capability == agents.ImageBatchCapability {
			central.agents.ImageBatch(id, gen, true)
		}
		if capability == agents.StatsCapability {
			central.agents.Stats(id, gen, true)
		}
		if capability == agents.ContainersCapability {
			central.agents.Containers(id, gen, true)
		}
		if capability == agents.NetworksCapability {
			central.agents.Networks(id, gen, true)
		}
		if capability == agents.VolumeBackupsCapability {
			central.agents.VolumeBackups(id, gen, true)
		}
		if capability == agents.VolumesCapability {
			central.agents.Volumes(id, gen, true)
		}
		if capability == agents.ImagesCapability {
			central.agents.Images(id, gen, true)
		}
	}
	central.relay.Connect(id, gen)
	rows, _ := backend.discovery.List(context.Background())
	central.agents.Publish(id, key, agents.Update{Protocol: agents.Protocol, Generation: gen, Sequence: 1, Containers: rows})
	ctx, stop := context.WithCancel(context.Background())
	var jobs sync.WaitGroup
	pollDone := make(chan struct{})
	var mu sync.Mutex
	running := map[string]context.CancelFunc{}
	go func() {
		defer close(pollDone)
		for ctx.Err() == nil {
			batch, err := central.relay.Poll(ctx, id, gen)
			if err != nil {
				return
			}
			mu.Lock()
			for _, cancelID := range batch.Cancel {
				if cancel := running[cancelID]; cancel != nil {
					cancel()
				}
			}
			for _, command := range batch.Commands {
				job, cancel := context.WithCancel(ctx)
				running[command.ID] = cancel
				jobs.Add(1)
				go func(command agents.Command) {
					defer jobs.Done()
					writer := &relayTestWriter{ctx: job, relay: central.relay, host: id, gen: gen, id: command.ID, header: http.Header{}}
					a := &AgentBackend{server: backend, handler: backend.Handler()}
					a.Serve(job, writer, command)
					if writer.status == 0 {
						writer.WriteHeader(200)
					}
					writer.send(agents.Frame{Done: true})
					cancel()
				}(command)
			}
			mu.Unlock()
		}
	}()
	cancel := func() { stop(); central.relay.Disconnect(id); backend.closeConsoles() }
	t.Cleanup(func() { cancel(); <-pollDone; jobs.Wait() })
	return id, cancel
}
func TestRemoteProjectsAreExecutedAndPersistedOnlyOnSelectedHost(t *testing.T) {
	local, localProject, _ := fixture(t, &fakeExecutor{})
	central := newServer(local, Config{AgentsEnabled: true}, "long-password-123456")
	central.agents, _ = agents.Open(t.TempDir())
	central.sessions[sessionKey("browser")] = session{CSRF: "browser-csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "browser"}
	remoteExec := &fakeExecutor{}
	remote, project, rev := fixture(t, remoteExec)
	backend := newServer(remote, Config{}, "unused-password")
	backend.discovery = &logStub{inventoryStub: inventoryStub{rows: []docker.Container{{ID: strings.Repeat("a", 64), Name: "remote-container", State: "running", ComposeProject: project.ProjectName}}}}
	host, disconnect := relayAgentFixture(t, central, backend, "remote")
	h := central.Handler()
	if localProject.ID != project.ID {
		t.Fatal("fixture must use colliding project IDs")
	}
	state := call(h, "GET", "/api/state?host="+host, "", "", cookie)
	if state.Code != 200 || !strings.Contains(state.Body.String(), project.Directory) || strings.Contains(state.Body.String(), localProject.Directory) {
		t.Fatal(state.Code, state.Body.String())
	}
	body := url.Values{"csrf": {"browser-csrf"}, "id": {project.ID}, "revision": {rev}, "action": {"save"}, "content": {"services: {}\n"}}.Encode()
	response := call(h, "POST", "/api/action?host="+host, body, "http://reef.test", cookie)
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	remote.wg.Wait()
	if remote.Projects[project.ID].Op.Phase != "completed" || local.Projects[localProject.ID].Op.ID != "" {
		t.Fatal("operation crossed hosts")
	}
	logs := call(h, "GET", "/api/container-logs?host="+host+"&id="+strings.Repeat("a", 64), "", "", cookie)
	if logs.Code != 200 || !strings.Contains(logs.Body.String(), "application output") {
		t.Fatal(logs.Code, logs.Body.String())
	}
	inventory := call(h, "GET", "/api/containers?host="+host, "", "", cookie)
	if inventory.Code != 200 || !strings.Contains(inventory.Body.String(), "managed_project_id") || !strings.Contains(inventory.Body.String(), host) {
		t.Fatal(inventory.Code, inventory.Body.String())
	}
	for _, action := range []string{"apply", "stop", "start"} {
		currentRev, err := revision(project)
		if err != nil {
			t.Fatal(err)
		}
		form := url.Values{"csrf": {"browser-csrf"}, "id": {project.ID}, "revision": {currentRev}, "action": {action}, "content": {"services: {}\n"}}
		result := call(h, "POST", "/api/action?host="+host, form.Encode(), "http://reef.test", cookie)
		if result.Code != 202 {
			t.Fatal(action, result.Code, result.Body.String())
		}
		remote.wg.Wait()
		if remote.Projects[project.ID].Op.Phase != "completed" {
			t.Fatal(action, remote.Projects[project.ID].Op.Result)
		}
	}
	remote.Images = &ImageService{Exec: &imageExecutor{}}
	checked := call(h, "POST", "/api/image-updates?host="+host, url.Values{"id": {project.ID}, "csrf": {"browser-csrf"}}.Encode(), "http://reef.test", cookie)
	if checked.Code != 202 {
		t.Fatal(checked.Code, checked.Body.String())
	}
	remote.wg.Wait()
	if len(remote.Projects[project.ID].ImageUpdates.Services) != 1 || remote.Projects[project.ID].ImageUpdates.Services[0].Status != "available" {
		t.Fatal("image check not executed on remote")
	}
	if local.Projects[localProject.ID].Op.ID != "" {
		t.Fatal("remote actions reached local")
	}
	disconnect()
	response = call(h, "GET", "/api/document?host="+host+"&id="+project.ID, "", "", cookie)
	if response.Code != 503 && response.Code != 409 {
		t.Fatal("disconnected host fell back", response.Code)
	}
}
func TestRemoteConsoleKeepsCentralOwnershipAndMultiplexing(t *testing.T) {
	central, localReader, cookie := consoleFixture(t)
	central.cfg.AgentsEnabled = true
	central.agents, _ = agents.Open(t.TempDir())
	remote, reader, _ := consoleFixture(t)
	host, _ := relayAgentFixture(t, central, remote, "remote")
	request := httptest.NewRequest("GET", "http://reef.test/?host="+host, nil)
	request.AddCookie(cookie)
	peer, err := central.remotePeer(request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	terminal, err := peer.OpenConsole(ctx, strings.Repeat("a", 64), "/bin/sh", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if localReader.opens != 0 || reader.opens != 1 {
		t.Fatal("console opened on wrong Docker engine")
	}
	if _, err = terminal.Write([]byte("printf remote\n")); err != nil {
		t.Fatal(err)
	}
	if err = terminal.Resize(ctx, 90, 30); err != nil {
		t.Fatal(err)
	}
	reader.terminal.mu.Lock()
	input := reader.terminal.input.String()
	cols := reader.terminal.cols
	reader.terminal.mu.Unlock()
	if input != "printf remote\n" || cols != 90 {
		t.Fatal(input, cols)
	}
	go reader.terminal.w.Write([]byte("remote output"))
	buffer := make([]byte, 64)
	n, err := terminal.Read(buffer)
	if err != nil || string(buffer[:n]) != "remote output" {
		t.Fatal(n, err, string(buffer[:n]))
	}
	terminal.Close()
	select {
	case <-reader.terminal.closed:
	case <-ctx.Done():
		t.Fatal("remote shell survived stream cancellation")
	}
}
func TestAgentBackendRejectsOtherAPIsAndUsesLocalRoots(t *testing.T) {
	m, _, _ := fixture(t, &fakeExecutor{})
	s := newServer(m, Config{}, "unused-password")
	backend := &AgentBackend{server: s, handler: s.Handler()}
	for _, path := range []string{"/login", "/logout", "/api/hosts", "/agent/v1/enroll"} {
		w := httptest.NewRecorder()
		backend.Serve(context.Background(), w, agents.Command{Owner: strings.Repeat("a", 64), Expires: time.Now().Add(time.Hour), Method: "POST", Path: path})
		if w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	backend.Serve(context.Background(), w, agents.Command{Owner: strings.Repeat("a", 64), Expires: time.Now().Add(time.Hour), Method: "GET", Path: "/api/state", Query: url.Values{"host": {"not-local"}}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var rows []map[string]any
	if json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != 1 {
		t.Fatal(w.Body.String())
	}
	_ = io.Discard
}

func TestRemoteRegistrationAndRootsRemainOnAgent(t *testing.T) {
	local, _, _ := fixture(t, &fakeExecutor{})
	central := newServer(local, Config{AgentsEnabled: true}, "long-password-123456")
	central.agents, _ = agents.Open(t.TempDir())
	central.sessions[sessionKey("browser")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "browser"}
	root := t.TempDir()
	installation := filepath.Join(root, "reef-agent")
	projectDir := filepath.Join(root, "app")
	os.MkdirAll(installation, 0700)
	os.MkdirAll(projectDir, 0700)
	os.WriteFile(filepath.Join(projectDir, "compose.yaml"), []byte("services: {}\n"), 0600)
	cfg := Config{StateDir: t.TempDir(), InstallationDirectory: installation, Projects: []Project{}}
	manager, err := newManager(cfg, &fakeExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	backend := newServer(manager, cfg, "unused-password")
	backend.discovery = &inventoryStub{rows: []docker.Container{{ID: strings.Repeat("a", 64), Name: "app", State: "running", ComposeProject: "app", WorkingDir: projectDir, ConfigFiles: filepath.Join(projectDir, "compose.yaml")}}}
	host, _ := relayAgentFixture(t, central, backend, "managed-host")
	h := central.Handler()
	proposal := call(h, "GET", "/api/projects/proposal?host="+host+"&name=app", "", "", cookie)
	if proposal.Code != 200 || !strings.Contains(proposal.Body.String(), projectDir) {
		t.Fatal(proposal.Code, proposal.Body.String())
	}
	form := url.Values{"csrf": {"csrf"}, "confirmed": {"yes"}, "project_name": {"app"}, "name": {"App"}, "directory": {projectDir}, "compose_files": {"compose.yaml"}}
	form.Set("directory", t.TempDir())
	rejected := call(h, "POST", "/api/projects/register?host="+host, form.Encode(), "http://reef.test", cookie)
	if rejected.Code == 201 || rejected.Code == 200 {
		t.Fatal("root was broadened centrally")
	}
	form.Set("directory", projectDir)
	registered := call(h, "POST", "/api/projects/register?host="+host, form.Encode(), "http://reef.test", cookie)
	if registered.Code != 201 {
		t.Fatal(registered.Code, registered.Body.String())
	}
	var project Project
	json.Unmarshal(registered.Body.Bytes(), &project)
	if _, ok := local.get(project.ID); ok {
		t.Fatal("remote registration persisted centrally")
	}
	if _, ok := manager.get(project.ID); !ok {
		t.Fatal("remote registration missing")
	}
	if _, err := os.Stat(filepath.Join(manager.StateDir, "registered-projects.json")); err != nil {
		t.Fatal("registry not durable", err)
	}
	removed := call(h, "POST", "/api/projects/unmanage?host="+host, url.Values{"csrf": {"csrf"}, "confirmed": {"yes"}, "id": {project.ID}}.Encode(), "http://reef.test", cookie)
	if removed.Code != 204 {
		t.Fatal(removed.Code, removed.Body.String())
	}
	if len(manager.snapshot()) != 0 {
		t.Fatal("remote unmanagement failed")
	}
}
func TestLocalAndRemoteConsolesShareOneCentralStreamAndQuota(t *testing.T) {
	central, _, cookie := consoleFixture(t)
	central.cfg.AgentsEnabled = true
	central.cfg.ConsoleSessionsPerLogin = 2
	central.agents, _ = agents.Open(t.TempDir())
	remote, reader, _ := consoleFixture(t)
	host, _ := relayAgentFixture(t, central, remote, "remote")
	server := httptest.NewServer(central.Handler())
	defer server.Close()
	post := func(path string, form url.Values) *http.Response {
		t.Helper()
		r, _ := http.NewRequest("POST", server.URL+path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", server.URL)
		r.AddCookie(cookie)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	stream := post("/api/console/stream", url.Values{"csrf": {"owner"}})
	defer stream.Body.Close()
	decoder := json.NewDecoder(stream.Body)
	var transport struct{ Type, ID string }
	if err := decoder.Decode(&transport); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"local", host} {
		form := consoleForm()
		form.Set("transport", transport.ID)
		form.Set("client", target)
		response := post("/api/console/open?host="+target, form)
		if response.StatusCode != 200 {
			data, _ := io.ReadAll(response.Body)
			t.Fatal(response.StatusCode, string(data))
		}
		response.Body.Close()
	}
	ids := map[string]string{}
	for i := 0; i < 2; i++ {
		var ready struct{ Type, Client, Console string }
		if err := decoder.Decode(&ready); err != nil || ready.Type != "ready" {
			t.Fatal(ready, err)
		}
		ids[ready.Client] = ready.Console
	}
	form := consoleForm()
	form.Set("transport", transport.ID)
	form.Set("client", "third")
	response := post("/api/console/open?host="+host, form)
	response.Body.Close()
	if response.StatusCode != 429 {
		t.Fatal("remote bypassed global quota", response.StatusCode)
	}
	response = post("/api/console/input?host=local", url.Values{"csrf": {"owner"}, "console": {ids[host]}, "data": {"eA=="}})
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("console crossed explicit host scope", response.StatusCode)
	}
	go reader.terminal.w.Write([]byte("remote output"))
	var frame struct{ Type, Client string }
	if err := decoder.Decode(&frame); err != nil || frame.Type != "output" || frame.Client != host {
		t.Fatal(frame, err)
	}
	stream.Body.Close()
	select {
	case <-reader.terminal.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("remote console survived panel close")
	}
}

func TestAgentBackendInstallationParentIsConfiguredOnTheAgent(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	installation := filepath.Join(root, "reef-agent")
	if err = os.Mkdir(installation, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REEF_INSTALLATION_DIR", installation)
	backend, err := NewAgentBackend("", filepath.Join(t.TempDir(), "managed"), "/missing-docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	roots := allowedRoots(backend.server.cfg)
	if len(roots) != 1 || roots[0] != root {
		t.Fatal("wrong agent roots", roots)
	}
	if err = authorizedDirectory(backend.server.cfg, installation); err == nil {
		t.Fatal("agent could adopt itself")
	}
	if err = authorizedDirectory(backend.server.cfg, t.TempDir()); err == nil {
		t.Fatal("agent could escape its own roots")
	}
}
