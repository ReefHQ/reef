package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reef/internal/agents"
	"reef/internal/docker"
	"reef/internal/notifications"
	"strings"
	"time"
)

type agentSessionKey struct{}
type AgentBackend struct {
	events  *notifications.Service
	server  *Server
	handler http.Handler
	stop    context.CancelFunc
}

func NewAgentBackend(configPath, stateDir, socket string, updateDirs ...string) (*AgentBackend, error) {
	c := Config{StateDir: stateDir, AllowedProjectRoots: []string{}, Projects: []Project{}}
	var err error
	if configPath != "" {
		c, err = loadConfigState(configPath, stateDir)
		if err != nil {
			return nil, err
		}
	}
	if configPath == "" {
		if os.Getenv("REEF_INSTALLATION_DIR") != "" {
			c.AllowedProjectRoots = nil
		}
		c, err = finishConfig(c)
		if err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(stateDir) {
		return nil, errors.New("agent managed state must be absolute")
	}
	c.SecureCookie = false
	c.AgentsEnabled = false
	executor := DockerExecutor{Socket: socket}
	m, err := newManager(c, executor)
	if err != nil {
		return nil, err
	}
	s := newServer(m, c, randomID())
	s.discovery = docker.NewClient(socket)
	ctx, stop := context.WithCancel(context.Background())
	m.Images = &ImageService{Exec: executor}
	m.initVolumeBackups(c)
	a := &AgentBackend{server: s, handler: s.Handler(), stop: stop}
	updateDir := os.Getenv("REEF_AGENT_UPDATE_DIR")
	if len(updateDirs) > 0 {
		updateDir = updateDirs[0]
	}
	a.events, err = notifications.Open(stateDir, true)
	if err != nil {
		a.events = notifications.Unavailable(true)
	}
	m.Notify = a.events.Emit
	if err = a.configureAgentUpdates(ctx, updateDir); err != nil {
		if m.Backups.store != nil {
			m.Backups.store.Close()
		}
		stop()
		return nil, err
	}
	m.startImageChecks(ctx)
	return a, nil
}
func (a *AgentBackend) CloseConsoles() { a.server.closeConsoles() }
func (a *AgentBackend) Close() {
	a.stop()
	a.CloseConsoles()
	a.server.m.wg.Wait()
	if b := a.server.m.Backups; b != nil && b.store != nil {
		_ = b.store.Close()
	}
}
func (a *AgentBackend) Serve(ctx context.Context, w http.ResponseWriter, command agents.Command) {
	if ctx.Err() != nil {
		http.Error(w, "Agent command cancelled", 503)
		return
	}
	if !agents.AllowedCommand(command.Method, command.Path) || !docker.ValidContainerID(command.Owner) || time.Until(command.Expires) <= 0 || time.Until(command.Expires) > 12*time.Hour {
		http.Error(w, "Unsupported agent command", 400)
		return
	}
	query := url.Values{}
	for key, values := range command.Query {
		if key != "host" {
			query[key] = values
		}
	}
	form := url.Values{}
	for key, values := range command.Form {
		if key != "csrf" {
			form[key] = values
		}
	}
	form.Set("csrf", command.Owner)
	target := "http://reef-agent.internal" + command.Path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(context.WithValue(ctx, agentSessionKey{}, session{CSRF: command.Owner, Expires: command.Expires}), command.Method, target, strings.NewReader(form.Encode()))
	if err != nil {
		http.Error(w, "Invalid agent command", 400)
		return
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://reef-agent.internal")
	a.handler.ServeHTTP(w, request)
}
