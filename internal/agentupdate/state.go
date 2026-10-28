// Package agentupdate coordinates explicitly requested, host-local agent updates.
package agentupdate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const Repository = "registry.gitlab.renaudbaivier.com/reef/reef/agent"

var stable = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var revision = regexp.MustCompile(`^[0-9a-f]{40}$`)

func ValidTarget(version, rev string) bool {
	return len(version) <= 64 && stable.MatchString(version) && revision.MatchString(rev)
}

func Newer(target, current string) bool {
	if !stable.MatchString(target) || !stable.MatchString(current) {
		return false
	}
	a, b := strings.Split(target, "."), strings.Split(current, ".")
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return len(a[i]) > len(b[i])
		}
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

type Operation struct {
	ID          string    `json:"id"`
	Version     string    `json:"version"`
	FromVersion string    `json:"from_version"`
	Revision    string    `json:"revision"`
	Phase       string    `json:"phase"`
	Message     string    `json:"message"`
	Started     time.Time `json:"started"`
	Updated     time.Time `json:"updated"`
}

func (o Operation) Running() bool {
	switch o.Phase {
	case "queued", "pulling", "recreating", "reconnecting":
		return true
	}
	return false
}

type Connection struct {
	Version  string    `json:"version"`
	Revision string    `json:"revision"`
	At       time.Time `json:"at"`
}
type Heartbeat struct {
	At time.Time `json:"at"`
}
type Store struct{ Dir string }

func Open(dir string) (*Store, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) == "/" {
		return nil, errors.New("Update state must be an absolute private directory")
	}
	for p := filepath.Clean(dir); ; p = filepath.Dir(p) {
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("Update state must not contain symlinks")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	return &Store{dir}, nil
}
func (s *Store) Read(name string, out any) error {
	p := filepath.Join(s.Dir, name)
	info, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return errors.New("Unsafe update state file")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
func (s *Store) Write(name string, value any) error {
	p := filepath.Join(s.Dir, name)
	if info, err := os.Lstat(p); err == nil && !info.Mode().IsRegular() {
		return errors.New("Unsafe update state file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, ".update-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err == nil {
		err = os.Rename(f.Name(), p)
	}
	if err != nil {
		return err
	}
	dir, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Store) Available() bool {
	var h Heartbeat
	return s.Read("heartbeat.json", &h) == nil && time.Since(h.At) >= 0 && time.Since(h.At) < 15*time.Second
}
func (s *Store) Operation() (Operation, error) {
	var o Operation
	err := s.Read("operation.json", &o)
	if os.IsNotExist(err) {
		return o, nil
	}
	return o, err
}

// Both processes take this lock when admitting or recovering a request.
func (s *Store) Lock(name string) (func(), error) {
	p := filepath.Join(s.Dir, name)
	if info, err := os.Lstat(p); err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("Unsafe update lock")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("Agent update already running")
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
