package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"reef/internal/accounts"
	"reef/internal/agents"
)

func (s *Server) principal(sess session) accounts.User {
	if s.accounts == nil {
		return accounts.User{Role: "admin", AllHosts: true}
	}
	u, _ := s.accounts.User(sess.UserID)
	return u
}
func (s *Server) requestUser(r *http.Request) accounts.User {
	sess, _ := s.getSession(r)
	return s.principal(sess)
}
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, sess session) bool {
	if s.accounts == nil {
		return true
	}
	u := s.principal(sess)
	if u.Disabled || u.ID == "" || u.Revision != sess.Revision {
		http.Error(w, "Sign-in required", 401)
		return false
	}
	allowed := false
	path := r.URL.Path
	host := r.URL.Query().Get("host")
	if host == "" {
		host = "local"
	}
	switch path {
	case "/", "/api/me", "/logout":
		allowed = true
	case "/api/users", "/api/webhook":
		allowed = u.GlobalAdmin()
	case "/api/hosts":
		allowed = r.Method == "GET" || u.GlobalAdmin()
	case "/api/history":
		allowed = r.Method == "GET" && (host == "all" || u.Allows(host))
	case "/api/schedules":
		allowed = (u.Allows(host) || (r.Method == "GET" && r.URL.Query().Get("all") == "true")) && (r.Method == "GET" || u.Role == "admin")
	case "/api/monitoring":
		allowed = r.Method == "GET" || u.GlobalAdmin()
	case "/api/console/stream":
		allowed = u.Role == "admin"
	case "/api/console/input", "/api/console/resize", "/api/console/close":
		s.mu.Lock()
		entry := s.consoles[r.FormValue("console")]
		if entry == nil && path == "/api/console/close" {
			for _, c := range s.consoles {
				if c.owner == sess.CSRF && c.transport == r.FormValue("transport") && c.client == r.FormValue("client") {
					entry = c
					break
				}
			}
		}
		if entry != nil && entry.owner == sess.CSRF {
			host = entry.host
			if host == "" {
				host = "local"
			}
			allowed = u.Role == "admin" && u.Allows(host)
		}
		s.mu.Unlock()
	case "/console", "/api/console/open", "/api/agent-update", "/api/projects/proposal":
		allowed = u.Role == "admin" && u.Allows(host)
	default:
		if agents.AllowedCommand(r.Method, path) {
			allowed = u.Allows(host) && (r.Method == "GET" || u.Role == "admin")
		}
	}
	if !allowed {
		http.Error(w, "Your account does not have permission for this host or action", 403)
	}
	return allowed
}

// Revalidate before every response chunk, including remote/delayed responses and
// console streams. ResponseController can unwrap the real streaming writer.
type permissionWriter struct {
	http.ResponseWriter
	allowed func() bool
	started bool
}

func (w *permissionWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *permissionWriter) WriteHeader(status int) {
	if !w.allowed() {
		if !w.started {
			w.started = true
			w.ResponseWriter.WriteHeader(401)
		}
		return
	}
	w.started = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *permissionWriter) Write(b []byte) (int, error) {
	if !w.allowed() {
		if !w.started {
			w.started = true
			w.ResponseWriter.WriteHeader(401)
		}
		return 0, io.ErrClosedPipe
	}
	w.started = true
	return w.ResponseWriter.Write(b)
}
func (s *Server) revokeUserSessions(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owners := map[string]bool{}
	next := s.nextSessions()
	for key, sess := range next {
		if sess.UserID == id {
			owners[sess.CSRF] = true
			delete(next, key)
		}
	}
	// Account revisions already revoke these cookies durably even if this cleanup
	// write fails. Active consoles are cancelled immediately.
	_ = s.saveSessions(next)
	s.sessions = next
	for _, entry := range s.consoles {
		if owners[entry.owner] {
			entry.cancel()
		}
	}
	for _, stream := range s.consoleStreams {
		if owners[stream.owner] {
			stream.cancel()
		}
	}
}
func (s *Server) initialHost(u accounts.User) string {
	if u.Allows("local") {
		return "local"
	}
	if s.agents != nil {
		for _, h := range s.agents.Views() {
			if h.Status != "revoked" && u.Allows(h.ID) {
				return h.ID
			}
		}
	}
	return "none"
}
func (s *Server) historyCursor(value string, u accounts.User, encode bool) (string, error) {
	if s.accounts == nil {
		return value, nil
	}
	key := s.accounts.Key()
	block, e := aes.NewCipher(key)
	if e != nil {
		return "", e
	}
	gcm, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	aad := []byte(u.ID)
	if encode {
		nonce := make([]byte, gcm.NonceSize())
		if _, e = rand.Read(nonce); e != nil {
			return "", e
		}
		return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(value), aad)), nil
	}
	raw, e := base64.RawURLEncoding.DecodeString(value)
	if e != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("History page expired; refresh the latest page")
	}
	b, e := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], aad)
	if e != nil {
		return "", errors.New("History page expired; refresh the latest page")
	}
	return string(b), nil
}
func (s *Server) filterHosts(u accounts.User, hosts []agents.View) []agents.View {
	out := []agents.View{}
	for _, h := range hosts {
		if u.Allows(h.ID) {
			out = append(out, h)
		}
	}
	return out
}
