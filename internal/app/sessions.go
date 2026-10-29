package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reef/internal/accounts"
	"time"
)

const sessionLifetime = 12 * time.Hour
const maxSessions = 256

type sessionState struct {
	Version  int                `json:"version"`
	Sessions map[string]session `json:"sessions"`
}
type signedSessionState struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

// Store only token fingerprints. The password-bound signature prevents editing
// the registry to mint sessions, and changing the password revokes old sessions.
func sessionKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (s *Server) sessionSignature(payload []byte) []byte {
	key := s.password[:]
	if s.accounts != nil {
		key = s.accounts.Key()
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("reef-auth-sessions-v1\x00"))
	mac.Write(payload)
	return mac.Sum(nil)
}

func (s *Server) sessionPath() (string, error) {
	if s.cfg.StateDir == "" {
		return "", nil
	} // In-memory test instances only.
	dir := filepath.Join(s.cfg.StateDir, "auth")
	if e := rejectSymlinks(dir); e != nil && !os.IsNotExist(e) {
		return "", errors.New("Authentication state path must not contain symlinks")
	}
	if e := os.Mkdir(dir, 0700); e != nil && !os.IsExist(e) {
		return "", errors.New("Cannot create private authentication state directory")
	}
	info, e := os.Stat(dir)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("Authentication state directory must be private (0700)")
	}
	path := filepath.Join(dir, "sessions.json")
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return "", errors.New("Authentication state must be a private regular file (0600)")
		}
	} else if !os.IsNotExist(e) {
		return "", errors.New("Cannot inspect authentication state")
	}
	return path, nil
}

// Called with Server.mu held for mutations, or before serving any requests.
func (s *Server) saveSessions(sessions map[string]session) error {
	path, e := s.sessionPath()
	if e != nil || path == "" {
		return e
	}
	version := 1
	if s.accounts != nil {
		version = 2
	}
	payload, e := json.Marshal(sessionState{Version: version, Sessions: sessions})
	if e != nil {
		return e
	}
	b, e := json.Marshal(signedSessionState{Payload: payload, Signature: hex.EncodeToString(s.sessionSignature(payload))})
	if e != nil {
		return e
	}
	if e = atomicWrite(path, b, 0600); e != nil {
		return errors.New("Cannot persist authentication state")
	}
	return nil
}

func (s *Server) restoreSessions() error {
	path, e := s.sessionPath()
	if e != nil || path == "" {
		return e
	}
	f, e := os.Open(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return errors.New("Cannot read authentication state")
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 128*1024+1))
	if e != nil {
		return errors.New("Cannot read authentication state")
	}
	var signed signedSessionState
	valid := len(b) <= 128*1024 && json.Unmarshal(b, &signed) == nil
	signature, e := hex.DecodeString(signed.Signature)
	valid = valid && e == nil && hmac.Equal(signature, s.sessionSignature(signed.Payload))
	var state sessionState
	valid = valid && json.Unmarshal(signed.Payload, &state) == nil && state.Version == s.sessionVersion() && len(state.Sessions) <= maxSessions
	if valid {
		for key, sess := range state.Sessions {
			fingerprint, e1 := hex.DecodeString(key)
			csrf, e2 := hex.DecodeString(sess.CSRF)
			if e1 != nil || len(fingerprint) != 32 || e2 != nil || len(csrf) != 24 {
				valid = false
				break
			}
		}
	}
	if !valid {
		// An old password or damaged registry grants no sessions. Replace it now
		// so reverting a password cannot resurrect its previous sessions.
		s.sessions = map[string]session{}
		return s.saveSessions(s.sessions)
	}
	s.sessions = map[string]session{}
	now := time.Now()
	for key, sess := range state.Sessions {
		if now.Before(sess.Expires) && s.userSessionValid(sess) {
			s.sessions[key] = sess
		}
	}
	if len(s.sessions) != len(state.Sessions) {
		return s.saveSessions(s.sessions)
	}
	return nil
}

func (s *Server) nextSessions() map[string]session {
	result := make(map[string]session, len(s.sessions)+1)
	now := time.Now()
	for key, sess := range s.sessions {
		if now.Before(sess.Expires) {
			result[key] = sess
		}
	}
	return result
}

func (s *Server) createSession() (string, error) { return s.createUserSession(accounts.User{}) }
func (s *Server) createUserSession(u accounts.User) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts != nil && !s.userSessionValid(session{UserID: u.ID, Revision: u.Revision}) {
		return "", errors.New("Account changed; sign in again")
	}
	next := s.nextSessions()
	owned := 0
	for _, sess := range next {
		if u.ID != "" && sess.UserID == u.ID {
			owned++
		}
	}
	if owned >= 32 || len(next) >= maxSessions {
		return "", errSessionLimit
	}
	id := randomID()
	next[sessionKey(id)] = session{CSRF: randomID(), Expires: time.Now().Add(sessionLifetime), UserID: u.ID, Revision: u.Revision}
	if e := s.saveSessions(next); e != nil {
		return "", e
	}
	s.sessions = next
	return id, nil
}

var errSessionLimit = errors.New("Too many sessions")

func (s *Server) removeSession(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.nextSessions()
	delete(next, sessionKey(token))
	if e := s.saveSessions(next); e != nil {
		return e
	}
	s.sessions = next
	return nil
}

func (s *Server) sessionVersion() int {
	if s.accounts != nil {
		return 2
	}
	return 1
}
func (s *Server) userSessionValid(sess session) bool {
	if s.accounts == nil {
		return true
	}
	u, ok := s.accounts.User(sess.UserID)
	return ok && !u.Disabled && u.Revision == sess.Revision
}
