package app

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reef/internal/docker"
	assets "reef/web"
)

var webFiles = assets.Files

const appName = "Reef" // Central display name; no distribution license has been selected.
type session struct {
	CSRF    string    `json:"csrf"`
	Expires time.Time `json:"expires"`
}
type attempt struct {
	Count int
	Since time.Time
}
type Server struct {
	discovery      docker.Lister
	m              *Manager
	cfg            Config
	password       [32]byte
	mu             sync.Mutex
	sessions       map[string]session
	attempts       map[string]attempt
	consoles       map[string]*consoleSession
	consoleStreams map[string]*consoleTransport
}

func newServer(m *Manager, c Config, password string) *Server {
	return &Server{discovery: docker.NewClient("/var/run/docker.sock"), m: m, cfg: c, password: sha256.Sum256([]byte(password)), sessions: map[string]session{}, attempts: map[string]attempt{}}
}
func (s *Server) originOK(r *http.Request) bool {
	u, e := url.Parse(r.Header.Get("Origin"))
	if e != nil || u.Host != r.Host || u.User != nil {
		return false
	}
	scheme := "http"
	if s.cfg.SecureCookie {
		scheme = "https"
	}
	return u.Scheme == scheme
}
func (s *Server) getSession(r *http.Request) (session, bool) {
	c, e := r.Cookie("qcu_session")
	if e != nil {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(c.Value)
	v, ok := s.sessions[key]
	if ok && time.Now().After(v.Expires) {
		delete(s.sessions, key)
		return session{}, false
	}
	return v, ok
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(webFiles))))
	mux.HandleFunc("/login", s.login)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.getSession(r)
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "Sign-in required", 401)
			} else {
				http.Redirect(w, r, "/login", 303)
			}
			return
		}
		if r.Method != "GET" {
			if r.Method != "POST" || !s.originOK(r) {
				http.Error(w, "Origin rejected", 403)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 3200000)
			if e := r.ParseForm(); e != nil {
				http.Error(w, "Invalid or oversized form", 400)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Form.Get("csrf")), []byte(sess.CSRF)) != 1 {
				http.Error(w, "Invalid CSRF token", 403)
				return
			}
		}
		switch r.URL.Path {
		case "/console":
			if r.Method != "GET" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.consolePage(w, r, sess)
		case "/api/console/open":
			if r.Method != "POST" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.openConsole(w, r, sess)
		case "/api/console/stream":
			if r.Method != "POST" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.consoleStream(w, r, sess)
		case "/api/console/input", "/api/console/resize", "/api/console/close":
			if r.Method != "POST" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.consoleAction(w, r, sess)
		case "/":
			if r.Method != "GET" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			per, _ := s.cfg.consoleLimits()
			s.render(w, "index.html", map[string]any{"Name": appName, "CSRF": sess.CSRF, "ConsoleSessionLimit": per})
		case "/api/containers":
			if r.Method != "GET" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.containers(w, r)
		case "/api/container-logs":
			if r.Method != "GET" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.containerLogs(w, r)
		case "/api/projects/proposal":
			if r.Method != "GET" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.proposal(w, r)
		case "/api/projects/register":
			if r.Method != "POST" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.register(w, r)
		case "/api/projects/unmanage":
			if r.Method != "POST" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.unmanage(w, r)
		case "/api/state":
			if r.Method != "GET" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			s.state(w)
		case "/api/document":
			if r.Method != "GET" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			p, ok := s.m.get(r.URL.Query().Get("id"))
			if !ok {
				http.NotFound(w, r)
				return
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.busy {
				http.Error(w, "Operation in progress", 409)
				return
			}
			rev, e := revision(p.Project)
			if e != nil {
				http.Error(w, "Cannot read file", 500)
				return
			}
			b, e := os.ReadFile(filepath.Join(p.Project.Directory, p.Project.ComposeFiles[0]))
			if e != nil || len(b) > 1024*1024 {
				http.Error(w, "Cannot read file, or file exceeds 1 MiB", 500)
				return
			}
			writeJSON(w, map[string]string{"revision": rev, "content": string(b)})
		case "/api/action":
			if r.Method != "POST" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			e := s.m.begin(r.Form.Get("id"), r.Form.Get("action"), r.Form.Get("revision"), r.Form.Get("content"))
			if e != nil {
				http.Error(w, e.Error(), 409)
				return
			}
			w.WriteHeader(202)
			fmt.Fprint(w, "Operation started")
		case "/logout":
			if r.Method != "POST" {
				http.Error(w, "Method not allowed", 405)
				return
			}
			c, _ := r.Cookie("qcu_session")
			if e := s.removeSession(c.Value); e != nil {
				http.Error(w, "Cannot save sign-out. Please try again.", 503)
				return
			}
			s.mu.Lock()
			for _, entry := range s.consoles {
				if entry.owner == sess.CSRF {
					entry.cancel()
				}
			}
			for _, stream := range s.consoleStreams {
				if stream.owner == sess.CSRF {
					stream.cancel()
				}
			}
			s.mu.Unlock()
			http.SetCookie(w, &http.Cookie{Name: "qcu_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.SecureCookie, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/login", 303)
		default:
			http.NotFound(w, r)
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Keep native same-origin form origins usable while suppressing cross-origin referrers.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	s.renderStatus(w, name, data, http.StatusOK)
}
func (s *Server) renderStatus(w http.ResponseWriter, name string, data any, status int) {
	t, e := template.ParseFS(webFiles, name)
	if e != nil {
		http.Error(w, "Invalid template", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if e = t.Execute(w, data); e != nil {
		log.Print("HTML rendering failed")
	}
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		s.render(w, "login.html", map[string]any{"Name": appName})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if !s.originOK(r) {
		http.Error(w, "Origin rejected", 403)
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.mu.Lock()
	a := s.attempts[ip]
	if time.Since(a.Since) > time.Minute {
		a = attempt{Since: time.Now()}
	}
	a.Count++
	if len(s.attempts) > 1024 {
		s.attempts = map[string]attempt{}
	}
	s.attempts[ip] = a
	s.mu.Unlock()
	if a.Count > 10 {
		http.Error(w, "Too many attempts. Try again in one minute.", 429)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	got := sha256.Sum256([]byte(r.Form.Get("password")))
	if subtle.ConstantTimeCompare(got[:], s.password[:]) != 1 {
		s.renderStatus(w, "login.html", map[string]any{"Name": appName, "Error": "Incorrect password"}, http.StatusUnauthorized)
		return
	}
	id, e := s.createSession()
	if e != nil {
		if e == errSessionLimit {
			http.Error(w, "Too many sessions", 429)
		} else {
			http.Error(w, "Cannot save sign-in. Please try again.", 503)
		}
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "qcu_session", Value: id, Path: "/", HttpOnly: true, Secure: s.cfg.SecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	http.Redirect(w, r, "/", 303)
}
func (s *Server) state(w http.ResponseWriter) {
	type row struct {
		ID               string    `json:"id"`
		Name             string    `json:"name"`
		Directory        string    `json:"directory"`
		Files            []string  `json:"files"`
		ProjectName      string    `json:"project_name"`
		Operation        Operation `json:"operation"`
		Busy             bool      `json:"busy"`
		PersistenceError string    `json:"persistence_error"`
		RegisteredInUI   bool      `json:"registered_in_ui"`
	}
	rows := []row{}
	for _, p := range s.m.snapshot() {
		registered := s.m.registeredID(p.Project.ProjectName) != ""
		p.mu.Lock()
		rows = append(rows, row{p.Project.ID, p.Project.Name, p.Project.Directory, p.Project.ComposeFiles, p.Project.ProjectName, p.Op, p.busy, p.persistError, registered})
		p.mu.Unlock()
	}
	writeJSON(w, rows)
}
