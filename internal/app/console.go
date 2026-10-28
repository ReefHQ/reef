package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"reef/internal/docker"
	"reef/internal/notifications"
)

const consoleLifetime = 30 * time.Minute
const consoleIdle = 10 * time.Minute
const maxConsoleOutput = 16 << 20

type consoleSession struct {
	owner      string
	host       string
	terminal   docker.Console
	cancel     context.CancelFunc
	lastActive time.Time // Protected by Server.mu.
	transport  string
	client     string
}

func (s *Server) consoleContainer(r *http.Request) (docker.Container, error) {
	id := r.FormValue("id")
	if !docker.ValidContainerID(id) {
		return docker.Container{}, fmt.Errorf("Invalid container ID.")
	}
	discovery, e := s.discoveryFor(r)
	if e != nil {
		return docker.Container{}, e
	}
	rows, e := discovery.List(r.Context())
	if e != nil {
		return docker.Container{}, fmt.Errorf("Docker inventory is unavailable.")
	}
	for _, row := range rows {
		if row.ID == id {
			if row.State != "running" {
				return docker.Container{}, fmt.Errorf("Console access requires a running container.")
			}
			return row, nil
		}
	}
	return docker.Container{}, fmt.Errorf("Container not found. Refresh the inventory.")
}

func (s *Server) consolePage(w http.ResponseWriter, r *http.Request, sess session) {
	embedded := r.URL.Query().Get("embedded") == "1"
	channel := r.URL.Query().Get("channel")
	if embedded {
		if !identifier.MatchString(channel) || len(channel) > 80 {
			http.Error(w, "Invalid console frame.", 400)
			return
		}
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'self'; form-action 'self'")
	}
	container, e := s.consoleContainer(r)
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	// xterm's renderer generates styles dynamically. This permission applies only
	// to the dedicated console document; script, origin and framing rules stay strict.
	if !embedded {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	}
	hostName := "Local"
	if id := r.URL.Query().Get("host"); id != "" && id != "local" {
		if view, _, ok := s.agents.Inventory(id); ok {
			hostName = view.Name
		}
	}
	s.render(w, "console.html", map[string]any{"HostName": hostName, "Name": appName, "CSRF": sess.CSRF, "Container": container, "Embedded": embedded, "Channel": channel, "HostID": r.URL.Query().Get("host")})
}

func (s *Server) openConsole(w http.ResponseWriter, r *http.Request, sess session) {
	if r.Form.Get("confirmed") != "yes" {
		http.Error(w, "Confirm console access before connecting.", 400)
		return
	}
	shell := r.Form.Get("shell")
	cols, e1 := strconv.Atoi(r.Form.Get("cols"))
	rows, e2 := strconv.Atoi(r.Form.Get("rows"))
	if !docker.ValidShell(shell) || e1 != nil || e2 != nil || !docker.ValidConsoleSize(cols, rows) {
		http.Error(w, "Invalid console options.", 400)
		return
	}
	container, e := s.consoleContainer(r)
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	discovery, discoveryErr := s.discoveryFor(r)
	if discoveryErr != nil {
		http.Error(w, discoveryErr.Error(), 503)
		return
	}
	opener, ok := discovery.(docker.ConsoleOpener)
	if !ok {
		http.Error(w, "Console access is unavailable.", 503)
		return
	}
	expires := time.Now().Add(consoleLifetime)
	if sess.Expires.Before(expires) {
		expires = sess.Expires
	}
	parent := r.Context()
	transportID, clientID := r.Form.Get("transport"), r.Form.Get("client")
	var transport *consoleTransport
	if transportID != "" {
		s.mu.Lock()
		transport = s.consoleStreams[transportID]
		s.mu.Unlock()
		if transport == nil || transport.owner != sess.CSRF || transport.ctx.Err() != nil {
			http.Error(w, "Console transport not found.", 404)
			return
		}
		if !identifier.MatchString(clientID) || len(clientID) > 80 {
			http.Error(w, "Invalid console tab.", 400)
			return
		}
		parent = transport.ctx
	}
	ctx, cancel := context.WithDeadline(parent, expires)
	detached := false
	defer func() {
		if !detached {
			cancel()
		}
	}()
	stopOpening := context.AfterFunc(r.Context(), cancel)
	defer stopOpening()
	id := randomID()
	host := r.URL.Query().Get("host")
	if host == "" {
		host = "local"
	}
	entry := &consoleSession{host: host, owner: sess.CSRF, cancel: cancel, lastActive: time.Now(), transport: transportID, client: clientID}
	s.mu.Lock()
	if s.agentUpdating {
		s.mu.Unlock()
		http.Error(w, "Wait for the agent update before opening a console", 409)
		return
	}
	if s.consoles == nil {
		s.consoles = map[string]*consoleSession{}
	}
	owned := 0
	for _, c := range s.consoles {
		if transport != nil && c.transport == transportID && c.client == clientID {
			s.mu.Unlock()
			http.Error(w, "This console tab already has a session.", 409)
			return
		}
		if c.owner == sess.CSRF {
			owned++
		}
	}
	per, totalLimit := s.cfg.consoleLimits()
	if len(s.consoles) >= totalLimit || owned >= per {
		s.mu.Unlock()
		http.Error(w, "Console session limit reached. Disconnect an existing console first.", 429)
		return
	}
	s.consoles[id] = entry
	s.mu.Unlock()
	opened := false
	release := func() {
		s.mu.Lock()
		delete(s.consoles, id)
		s.mu.Unlock()
		cancel()
		if opened {
			s.m.emit(notifications.New("console", "session", "closed", id, container.Name))
		}
	}
	defer func() {
		if !detached {
			release()
		}
	}()
	_, remoteConsole := opener.(*remoteDocker)
	if !remoteConsole {
		s.m.emit(notifications.New("console", "session", "started", id, container.Name))
	}
	terminal, e := opener.OpenConsole(ctx, container.ID, shell, cols, rows)
	if e != nil {
		if !remoteConsole {
			s.m.emit(notifications.New("console", "session", "failed", id, container.Name))
		}
		http.Error(w, e.Error(), 503)
		return
	}
	defer func() {
		if !detached {
			terminal.Close()
		}
	}()
	if ctx.Err() != nil {
		if !remoteConsole {
			s.m.emit(notifications.New("console", "session", "unknown", id, container.Name))
		}
		return
	}
	opened = !remoteConsole
	s.mu.Lock()
	entry.terminal = terminal
	s.mu.Unlock()
	if transport != nil {
		detached = true
		go func() {
			defer release()
			defer terminal.Close()
			s.pumpConsole(ctx, terminal, entry, r, func(frame map[string]any) bool {
				frame["client"] = clientID
				frame["console"] = id
				select {
				case transport.events <- frame:
					return true
				case <-transport.ctx.Done():
					return false
				}
			})
		}()
		writeJSON(w, map[string]string{"id": id})
		return
	}
	// Cancelled HTTP requests must close a blocked Docker read/write too.
	stop := context.AfterFunc(ctx, func() { _ = terminal.Close() })
	defer stop()
	rc := http.NewResponseController(w)
	if e := rc.SetWriteDeadline(time.Time{}); e != nil {
		http.Error(w, "Streaming console responses are unavailable.", 503)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(w)
	send := func(value map[string]any) bool {
		// Bound each write even when the browser stops consuming output.
		if rc.SetWriteDeadline(time.Now().Add(15*time.Second)) != nil {
			return false
		}
		return encoder.Encode(value) == nil && rc.Flush() == nil
	}
	if !send(map[string]any{"type": "ready", "id": id, "name": container.Name}) {
		return
	}
	s.pumpConsole(ctx, terminal, entry, r, send)
}

func (s *Server) pumpConsole(ctx context.Context, terminal docker.Console, entry *consoleSession, r *http.Request, send func(map[string]any) bool) {
	stop := context.AfterFunc(ctx, func() { _ = terminal.Close() })
	defer stop()
	if entry.transport != "" {
		if !send(map[string]any{"type": "ready"}) {
			return
		}
	}
	type chunk struct {
		data []byte
		err  error
	}
	chunks := make(chan chunk, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, e := terminal.Read(buf)
			piece := chunk{append([]byte(nil), buf[:n]...), e}
			select {
			case chunks <- piece:
			case <-ctx.Done():
				return
			}
			if e != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	total := 0
	for {
		select {
		case <-ctx.Done():
			send(map[string]any{"type": "closed", "message": "Console disconnected or session expired."})
			return
		case <-ticker.C:
			_, active := s.getSession(r)
			s.mu.Lock()
			idle := time.Since(entry.lastActive) > consoleIdle
			s.mu.Unlock()
			if !active || idle {
				send(map[string]any{"type": "closed", "message": "Console session expired or was idle for 10 minutes."})
				return
			}
			if !send(map[string]any{"type": "heartbeat"}) {
				return
			}
		case piece := <-chunks:
			if len(piece.data) > 0 {
				total += len(piece.data)
				if total > maxConsoleOutput {
					send(map[string]any{"type": "closed", "message": "Console output limit reached. Reconnect to continue."})
					return
				}
				s.mu.Lock()
				entry.lastActive = time.Now()
				s.mu.Unlock()
				if !send(map[string]any{"type": "output", "data": base64.StdEncoding.EncodeToString(piece.data)}) {
					return
				}
			}
			if piece.err != nil {
				message := "Shell exited. Connect again to open a new shell."
				if piece.err != io.EOF {
					message = "Docker console connection ended."
				}
				send(map[string]any{"type": "closed", "message": message})
				return
			}
		}
	}
}

func (s *Server) consoleAction(w http.ResponseWriter, r *http.Request, sess session) {
	s.mu.Lock()
	entry := s.consoles[r.Form.Get("console")]
	if entry == nil && r.URL.Path == "/api/console/close" && r.Form.Get("transport") != "" {
		for _, c := range s.consoles {
			if c.transport == r.Form.Get("transport") && c.client == r.Form.Get("client") && c.owner == sess.CSRF {
				entry = c
				break
			}
		}
	}
	if entry != nil && r.URL.Query().Get("host") != "" {
		target := entry.host
		if target == "" {
			target = "local"
		}
		if target != r.URL.Query().Get("host") {
			s.mu.Unlock()
			http.Error(w, "Console session not found on this host.", 404)
			return
		}
	}
	if entry == nil || entry.owner != sess.CSRF || (entry.terminal == nil && r.URL.Path != "/api/console/close") {
		s.mu.Unlock()
		http.Error(w, "Console session not found.", 404)
		return
	}
	terminal := entry.terminal
	entry.lastActive = time.Now()
	s.mu.Unlock()
	var e error
	switch r.URL.Path {
	case "/api/console/close":
		entry.cancel()
	case "/api/console/input":
		b, err := base64.StdEncoding.DecodeString(r.Form.Get("data"))
		if err != nil || len(b) == 0 || len(b) > 16384 {
			http.Error(w, "Invalid terminal input.", 400)
			return
		}
		n, err := terminal.Write(b)
		e = err
		if e == nil && n != len(b) {
			e = io.ErrShortWrite
		}
	case "/api/console/resize":
		cols, e1 := strconv.Atoi(r.Form.Get("cols"))
		rows, e2 := strconv.Atoi(r.Form.Get("rows"))
		if e1 != nil || e2 != nil || !docker.ValidConsoleSize(cols, rows) {
			http.Error(w, "Invalid terminal size.", 400)
			return
		}
		e = terminal.Resize(r.Context(), cols, rows)
	}
	if e != nil {
		entry.cancel()
		http.Error(w, "Console connection ended.", 409)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) closeConsoles() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.consoles {
		entry.cancel()
	}
	for _, stream := range s.consoleStreams {
		stream.cancel()
	}
}
