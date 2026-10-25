// Package agents implements the inventory-only, outbound agent protocol.
package agents

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reef/internal/docker"
)

const Protocol = 1
const MaxPayload = 16 << 20

var ErrInvalid = errors.New("Invalid or expired agent credentials, enrollment or connection")

type Host struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	CredentialHash string `json:"credential_hash,omitempty"`
	Revoked        bool   `json:"revoked"`
}
type enrollment struct {
	Name    string
	Expires time.Time
}
type disk struct {
	Version int `json:"version"`
	Hosts   map[string]Host
	Tokens  map[string]enrollment
}
type snapshot struct {
	Notice     string
	Bytes      int
	Generation string
	Sequence   uint64
	Contact    time.Time
	Checked    time.Time
	Containers []docker.Container
	Error      string
	Version    string
}
type View struct {
	Notice      string    `json:"notice,omitempty"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	LastContact time.Time `json:"last_contact"`
	CheckedAt   time.Time `json:"checked_at"`
	Version     string    `json:"version"`
	Stale       bool      `json:"stale"`
	Error       string    `json:"error,omitempty"`
}
type Update struct {
	Protocol   int                `json:"protocol"`
	Generation string             `json:"generation"`
	Sequence   uint64             `json:"sequence"`
	Containers []docker.Container `json:"containers"`
	Error      string             `json:"error"`
}
type Registry struct {
	mu       sync.Mutex
	path     string
	data     disk
	live     map[string]snapshot
	attempts int
	window   time.Time
}

func secret() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func validName(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 100 && !strings.ContainsAny(s, "\x00\r\n")
}
func Open(state string) (*Registry, error) {
	r := &Registry{data: disk{Version: 1, Hosts: map[string]Host{}, Tokens: map[string]enrollment{}}, live: map[string]snapshot{}}
	if state == "" {
		return r, nil
	}
	dir := filepath.Join(state, "agents")
	// Reject symlinks in every existing component before creating private state.
	for p := dir; ; p = filepath.Dir(p) {
		if info, e := os.Lstat(p); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("Agent state must not contain symlinks")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	info, e := os.Stat(dir)
	if e != nil || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("Agent state directory must be private")
	}
	r.path = filepath.Join(dir, "registry.json")
	info, e = os.Lstat(r.path)
	if os.IsNotExist(e) {
		return r, nil
	}
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return nil, errors.New("Unsafe agent registry")
	}
	b, e := os.ReadFile(r.path)
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(b, &r.data); e != nil {
		return nil, e
	}
	if r.data.Version != 1 || r.data.Hosts == nil || r.data.Tokens == nil || len(r.data.Hosts) > 64 || len(r.data.Tokens) > 16 {
		return nil, errors.New("Invalid agent registry")
	}
	for id, h := range r.data.Hosts {
		if id != h.ID || len(id) != 64 || !validName(h.Name) || len(h.CredentialHash) != 64 {
			return nil, errors.New("Invalid agent registry host")
		}
	}
	return r, nil
}
func (r *Registry) save(d disk) error {
	if r.path == "" {
		r.data = d
		return nil
	}
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(r.path), ".registry-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), r.path); e != nil {
		return e
	}
	r.data = d
	// The registry is authoritative after rename; sync the directory for durability.
	dir, e := os.Open(filepath.Dir(r.path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func (r *Registry) clone() disk {
	d := disk{Version: 1, Hosts: map[string]Host{}, Tokens: map[string]enrollment{}}
	for id, h := range r.data.Hosts {
		d.Hosts[id] = h
	}
	for k, t := range r.data.Tokens {
		if time.Now().Before(t.Expires) {
			d.Tokens[k] = t
		}
	}
	return d
}
func (r *Registry) Token(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !validName(name) {
		return "", errors.New("Host name is required (maximum 100 characters)")
	}
	d := r.clone()
	if len(d.Tokens) >= 16 || len(d.Hosts) >= 64 {
		return "", errors.New("Agent enrollment limit reached")
	}
	token := secret()
	d.Tokens[hash(token)] = enrollment{name, time.Now().Add(10 * time.Minute)}
	return token, r.save(d)
}
func (r *Registry) Enroll(token string, protocol int) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.window) > time.Minute {
		r.window = time.Now()
		r.attempts = 0
	}
	r.attempts++
	if r.attempts > 30 {
		return "", "", ErrInvalid
	}
	t, ok := r.data.Tokens[hash(token)]
	if !ok || protocol != Protocol || !time.Now().Before(t.Expires) || len(r.data.Hosts) >= 64 {
		return "", "", ErrInvalid
	}
	d := r.clone()
	delete(d.Tokens, hash(token))
	id, key := secret(), secret()
	d.Hosts[id] = Host{ID: id, Name: t.Name, CredentialHash: hash(key)}
	if e := r.save(d); e != nil {
		return "", "", e
	}
	return id, key, nil
}
func (r *Registry) Authenticate(id, key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authorized(id, key)
}
func (r *Registry) authorized(id, key string) bool {
	h, ok := r.data.Hosts[id]
	return ok && !h.Revoked && key != "" && subtle.ConstantTimeCompare([]byte(h.CredentialHash), []byte(hash(key))) == 1
}
func (r *Registry) Begin(id, key, version string, protocol int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.authorized(id, key) {
		return "", ErrInvalid
	}
	if protocol != Protocol {
		v := r.live[id]
		v.Error = "Incompatible agent protocol"
		r.live[id] = v
		return "", errors.New("Incompatible agent protocol")
	}
	if len(version) > 100 {
		return "", ErrInvalid
	}
	v := r.live[id]
	if v.Generation != "" && time.Since(v.Contact) < 45*time.Second {
		v.Notice = "Connection replaced; check that this identity is used on only one host."
	}
	v.Generation = secret()
	v.Sequence = 0
	v.Contact = time.Now()
	v.Version = version
	r.live[id] = v
	return v.Generation, nil
}
func (r *Registry) Publish(id, key string, u Update) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.live[id]
	if !r.authorized(id, key) || u.Protocol != Protocol || u.Generation == "" || u.Generation != v.Generation || u.Sequence <= v.Sequence {
		return ErrInvalid
	}
	reject := func(message string) error {
		v.Error = message
		v.Contact = time.Now()
		r.live[id] = v
		return errors.New(message)
	}
	if len(u.Containers) > 10000 || len(u.Error) > 500 {
		return reject("Inventory exceeds limits")
	}
	seen := map[string]bool{}
	for _, c := range u.Containers {
		b, e := hex.DecodeString(c.ID)
		if e != nil || len(b) != 32 || hex.EncodeToString(b) != c.ID || seen[c.ID] {
			return reject("Invalid container ID")
		}
		seen[c.ID] = true
		switch c.State {
		case "created", "running", "paused", "restarting", "removing", "exited", "dead":
		default:
			return reject("Invalid container state")
		}
		for _, s := range []string{c.Name, c.Image, c.Status, c.ComposeProject, c.ComposeService} {
			if len(s) > 4096 || strings.ContainsRune(s, 0) {
				return reject("Invalid inventory field")
			}
		}
	}
	if u.Error == "" {
		b, _ := json.Marshal(u.Containers)
		total := len(b)
		for other, cached := range r.live {
			if other != id {
				total += cached.Bytes
			}
		}
		if total > 64<<20 {
			return reject("Central inventory cache limit reached")
		}
	}
	v.Contact = time.Now()
	v.Sequence = u.Sequence
	v.Error = u.Error
	if u.Error == "" {
		v.Containers = append([]docker.Container{}, u.Containers...)
		v.Checked = v.Contact
		encoded, _ := json.Marshal(u.Containers)
		v.Bytes = len(encoded)
	}
	r.live[id] = v
	return nil
}
func (r *Registry) Change(id, name string, revoke bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.clone()
	h, ok := d.Hosts[id]
	if !ok {
		return errors.New("Unknown host")
	}
	if !revoke {
		if !validName(name) {
			return errors.New("Invalid host name")
		}
		h.Name = name
	} else {
		h.Revoked = true
	}
	d.Hosts[id] = h
	if e := r.save(d); e != nil {
		return e
	}
	if revoke {
		delete(r.live, id)
	}
	return nil
}
func (r *Registry) view(h Host) View {
	v := r.live[h.ID]
	status := "connected"
	if time.Since(v.Contact) > 45*time.Second {
		status = "disconnected"
	}
	if h.Revoked {
		status = "revoked"
	}
	return View{Notice: v.Notice, ID: h.ID, Name: h.Name, Status: status, LastContact: v.Contact, CheckedAt: v.Checked, Version: v.Version, Stale: status != "connected" || v.Error != "" || time.Since(v.Checked) > 15*time.Second, Error: v.Error}
}
func (r *Registry) Views() []View {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := []View{}
	for _, h := range r.data.Hosts {
		rows = append(rows, r.view(h))
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name == rows[j].Name {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}
func (r *Registry) Inventory(id string) (View, []docker.Container, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.data.Hosts[id]
	if !ok {
		return View{}, nil, false
	}
	v := r.live[id]
	return r.view(h), append([]docker.Container{}, v.Containers...), true
}
