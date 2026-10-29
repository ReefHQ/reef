// Package accounts stores individual credentials and host authorization privately.
package accounts

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const Iterations = 600000
const MaxUsers = 128

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)
var keyID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var ErrState = errors.New("Account state unavailable or changed; inspect private auth state and restart Reef")
var ErrInput = errors.New("Use a unique lowercase username (3–64 characters), admin/viewer role and valid host assignments")

type User struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Role     string   `json:"role"`
	AllHosts bool     `json:"all_hosts"`
	Hosts    []string `json:"hosts"`
	Disabled bool     `json:"disabled"`
	Revision uint64   `json:"revision"`
}

func (u User) Allows(host string) bool {
	if u.Disabled {
		return false
	}
	if u.AllHosts {
		return true
	}
	for _, id := range u.Hosts {
		if id == host {
			return true
		}
	}
	return false
}
func (u User) GlobalAdmin() bool { return !u.Disabled && u.Role == "admin" && u.AllHosts }
func ValidHost(s string) bool    { return s == "local" || keyID.MatchString(s) }

type credential struct {
	User
	Salt       string `json:"salt"`
	Hash       string `json:"hash"`
	Iterations int    `json:"iterations"`
}
type disk struct {
	Version int          `json:"version"`
	Key     string       `json:"key"`
	Users   []credential `json:"users"`
}
type Store struct {
	mu     sync.Mutex
	path   string
	data   disk
	digest [32]byte
	exists bool
}

func secret() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func PasswordValid(p string) bool { return len(p) >= 16 && len(p) <= 1024 }
func hash(password, salt string) string {
	b, _ := hex.DecodeString(salt)
	key, e := pbkdf2.Key(sha256.New, password, b, Iterations, 32)
	if e != nil {
		panic(e)
	}
	return hex.EncodeToString(key)
}
func valid(u User) bool {
	if !keyID.MatchString(u.ID) || !identifier.MatchString(u.Username) || (u.Role != "admin" && u.Role != "viewer") || u.Revision == 0 || len(u.Hosts) > 65 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range u.Hosts {
		if !ValidHost(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func (s *Store) Key() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := hex.DecodeString(s.data.Key)
	return b
}
func (s *Store) User(id string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.data.Users {
		if c.ID == id {
			u := c.User
			u.Hosts = append([]string{}, u.Hosts...)
			return u, true
		}
	}
	return User{}, false
}
func (s *Store) List() []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []User{}
	for _, c := range s.data.Users {
		u := c.User
		u.Hosts = append([]string{}, u.Hosts...)
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

// Authentication performs the same KDF for unknown usernames and never holds the
// store mutex during expensive work. A revision check rejects concurrent changes.
func (s *Store) Authenticate(username, password string) (User, bool) {
	s.mu.Lock()
	candidate := credential{Salt: strings.Repeat("0", 64), Hash: strings.Repeat("0", 64), Iterations: Iterations}
	found := false
	for _, c := range s.data.Users {
		if c.Username == username {
			candidate = c
			found = true
			break
		}
	}
	s.mu.Unlock()
	if len(password) > 1024 {
		return User{}, false
	}
	got := hash(password, candidate.Salt)
	ok := subtle.ConstantTimeCompare([]byte(got), []byte(candidate.Hash)) == 1
	current, exists := s.User(candidate.ID)
	return current, found && exists && ok && !current.Disabled && current.Revision == candidate.Revision
}
func (s *Store) Change(u User, password string, remove bool) (User, error) {
	return s.change(u, password, remove, nil, false)
}
func (s *Store) ChangeBy(actor, u User, password string, remove, admin bool) (User, error) {
	return s.change(u, password, remove, &actor, admin)
}
func (s *Store) change(u User, password string, remove bool, actor *User, admin bool) (User, error) {
	if password != "" && !PasswordValid(password) {
		return User{}, errors.New("Passwords must contain 16–1024 bytes")
	}
	salt, hashed := "", ""
	if password != "" {
		salt = secret()
		hashed = hash(password, salt)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor != nil {
		authorized := false
		for _, c := range s.data.Users {
			if c.ID == actor.ID && !c.Disabled && c.Revision == actor.Revision {
				authorized = admin && c.GlobalAdmin() || !admin && !remove && reflect.DeepEqual(c.User, u)
			}
		}
		if !authorized {
			return User{}, errors.New("Account permissions changed; sign in again")
		}
	}
	old := s.data
	next := old
	next.Users = append([]credential{}, old.Users...)
	index := -1
	for i, c := range next.Users {
		if c.ID == u.ID && u.ID != "" {
			index = i
		}
	}
	if u.ID != "" && index < 0 {
		return User{}, ErrInput
	}
	if index < 0 {
		if len(next.Users) >= MaxUsers || password == "" || remove {
			return User{}, ErrInput
		}
		u.ID = secret()
		u.Revision = 1
		next.Users = append(next.Users, credential{})
		index = len(next.Users) - 1
	} else {
		if u.Revision != next.Users[index].Revision {
			return User{}, errors.New("Account changed; refresh before editing")
		}
		u.Revision = next.Users[index].Revision + 1
		if u.Revision == 0 {
			return User{}, ErrState
		}
	}
	u.Username = strings.TrimSpace(u.Username)
	u.Hosts = append([]string{}, u.Hosts...)
	if u.AllHosts {
		u.Hosts = []string{}
	}
	sort.Strings(u.Hosts)
	if !valid(u) {
		return User{}, ErrInput
	}
	for i, c := range next.Users {
		if i != index && c.Username == u.Username {
			return User{}, ErrInput
		}
	}
	if remove {
		next.Users = append(next.Users[:index], next.Users[index+1:]...)
	} else {
		entry := next.Users[index]
		entry.User = u
		if password != "" {
			entry.Salt = salt
			entry.Hash = hashed
			entry.Iterations = Iterations
		}
		next.Users[index] = entry
	}
	admins := 0
	for _, c := range next.Users {
		if c.GlobalAdmin() {
			admins++
		}
	}
	if admins == 0 {
		return User{}, errors.New("Keep at least one enabled administrator with access to all hosts")
	}
	if e := s.save(next); e != nil {
		return User{}, e
	}
	return u, nil
}
func (s *Store) ChangePassword(id, current, password string) error {
	if !PasswordValid(password) {
		return errors.New("Passwords must contain 16–1024 bytes")
	}
	u, ok := s.User(id)
	if !ok {
		return ErrInput
	}
	verified, ok := s.Authenticate(u.Username, current)
	if !ok || verified.ID != id {
		return errors.New("Current password is incorrect")
	}
	_, e := s.ChangeBy(verified, verified, password, false, false)
	return e
}
func (s *Store) Recover(username, password string) error {
	for _, u := range s.List() {
		if u.Username == username {
			u.Role = "admin"
			u.AllHosts = true
			u.Disabled = false
			_, e := s.Change(u, password, false)
			return e
		}
	}
	return errors.New("Recovery requires an existing username")
}
func privateParents(dir string) error {
	for p := dir; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
			return ErrState
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	i, e := os.Stat(dir)
	if e != nil || i.Mode().Perm()&0077 != 0 {
		return ErrState
	}
	return nil
}
func read(path string) ([]byte, bool, error) {
	i, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil, false, nil
	}
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 || i.Size() > 1<<20 {
		return nil, false, ErrState
	}
	b, e := os.ReadFile(path)
	if e != nil || len(b) > 1<<20 {
		return nil, false, ErrState
	}
	return b, true, nil
}
func Open(state, password string) (*Store, error) {
	if !filepath.IsAbs(state) {
		return nil, ErrState
	}
	dir := filepath.Join(state, "auth")
	for p := dir; ; p = filepath.Dir(p) {
		if i, e := os.Lstat(p); e == nil && i.Mode()&os.ModeSymlink != 0 {
			return nil, ErrState
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if os.MkdirAll(dir, 0700) != nil || privateParents(dir) != nil {
		return nil, ErrState
	}
	s := &Store{path: filepath.Join(dir, "users.json")}
	b, exists, e := read(s.path)
	if e != nil {
		return nil, e
	}
	marker := filepath.Join(dir, "users.initialized")
	marked, markedExists, e := read(marker)
	if e != nil || markedExists && string(marked) != "reef-users-v1\n" {
		return nil, ErrState
	}
	if !exists {
		if markedExists || !PasswordValid(password) {
			return nil, ErrState
		}
		salt := secret()
		u := credential{User: User{ID: secret(), Username: "admin", Role: "admin", AllHosts: true, Hosts: []string{}, Revision: 1}, Salt: salt, Hash: hash(password, salt), Iterations: Iterations}
		if e = s.save(disk{Version: 1, Key: secret(), Users: []credential{u}}); e != nil {
			return nil, e
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		var extra any
		var d disk
		if dec.Decode(&d) != nil || dec.Decode(&extra) != io.EOF || d.Version != 1 || !keyID.MatchString(d.Key) || len(d.Users) < 1 || len(d.Users) > MaxUsers {
			return nil, ErrState
		}
		ids, names := map[string]bool{}, map[string]bool{}
		admins := 0
		for _, c := range d.Users {
			if !valid(c.User) || !keyID.MatchString(c.Salt) || !keyID.MatchString(c.Hash) || c.Iterations != Iterations || ids[c.ID] || names[c.Username] {
				return nil, ErrState
			}
			ids[c.ID] = true
			names[c.Username] = true
			if c.GlobalAdmin() {
				admins++
			}
		}
		if admins == 0 {
			return nil, ErrState
		}
		s.data = d
		s.digest = sha256.Sum256(b)
		s.exists = true
	}
	if !markedExists {
		if e = writeAtomic(marker, []byte("reef-users-v1\n")); e != nil {
			return nil, e
		}
	}
	return s, nil
}
func writeAtomic(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".users-")
	if e != nil {
		return ErrState
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return ErrState
	}
	if os.Rename(f.Name(), path) != nil {
		return ErrState
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return ErrState
	}
	defer d.Close()
	if d.Sync() != nil {
		return ErrState
	}
	return nil
}
func (s *Store) save(next disk) error {
	if privateParents(filepath.Dir(s.path)) != nil {
		return ErrState
	}
	b, exists, e := read(s.path)
	if e != nil || exists != s.exists || exists && sha256.Sum256(b) != s.digest {
		return ErrState
	}
	b, e = json.Marshal(next)
	if e != nil || len(b) > 1<<20 {
		return ErrState
	}
	// Re-read after any error: an atomic rename may already be authoritative even
	// if directory sync failed. Never keep an old permission set after replacement.
	e = writeAtomic(s.path, b)
	current, present, re := read(s.path)
	if re == nil && present && bytes.Equal(current, b) {
		s.data = next
		s.digest = sha256.Sum256(b)
		s.exists = true
	}
	return e
}
