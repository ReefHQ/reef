package monitoring

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

const maxBytes = 4 << 20

func errorsConfig() error {
	return errors.New("Use disconnect 60–3600 s, unhealthy 30–3600 s, 2–100 restarts in 60–3600 s, recovery 30–600 s")
}
func sameDisk(a, b disk) bool { return reflect.DeepEqual(a, b) }
func parents(dir string) error {
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
func Open(state string) (*Store, error) {
	if !filepath.IsAbs(state) {
		return nil, ErrState
	}
	dir := filepath.Join(state, "monitoring")
	for p := dir; ; p = filepath.Dir(p) {
		if i, e := os.Lstat(p); e == nil && i.Mode()&os.ModeSymlink != 0 {
			return nil, ErrState
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if os.MkdirAll(dir, 0700) != nil || parents(dir) != nil {
		return nil, ErrState
	}
	s := Unavailable()
	s.persistenceError = false
	s.file = filepath.Join(dir, "state.json")
	raw, exists, e := read(s.file)
	if e != nil {
		return nil, e
	}
	if !exists {
		return s, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	var loaded disk
	if d.Decode(&loaded) != nil || d.Decode(&extra) != io.EOF || loaded.Version != 1 || !loaded.Config.Valid() || loaded.Holds == nil || len(loaded.Holds) > 65 || len(loaded.Incidents) > MaxIncidents || len(loaded.Pending) > MaxPending {
		return nil, ErrState
	}
	s.data = loaded
	ids := map[string]bool{}
	for _, v := range s.data.Incidents {
		if !episodeID.MatchString(v.ID) || ids[v.ID] || !ValidHost(v.HostID) || !validKind(v.Kind) || len(v.Host) > 800 || len(v.Target) > 640 || !hexID.MatchString(v.TargetID) || v.Opened.IsZero() || v.Updated.IsZero() {
			return nil, ErrState
		}
		ids[v.ID] = true
		switch v.Status {
		case "firing", "resolved", "retired":
		default:
			return nil, ErrState
		}
	}
	for _, e := range s.data.Pending {
		if !e.Valid() || e.Scope != "monitor" || !ValidHost(e.HostID) {
			return nil, ErrState
		}
	}
	for id, until := range s.data.Holds {
		if !ValidHost(id) || until.After(time.Now().Add(15*time.Minute)) {
			return nil, ErrState
		}
	}
	s.digest = sha256.Sum256(raw)
	s.existed = true
	return s, nil
}
func read(path string) ([]byte, bool, error) {
	i, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil, false, nil
	}
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 || i.Size() > maxBytes {
		return nil, false, ErrState
	}
	raw, e := os.ReadFile(path)
	if e != nil || len(raw) > maxBytes {
		return nil, false, ErrState
	}
	return raw, true, nil
}
func (s *Store) saveLocked() error {
	fail := func() error { s.persistenceError = true; return ErrState }
	if s.file == "" || parents(filepath.Dir(s.file)) != nil {
		return fail()
	}
	raw, exists, e := read(s.file)
	if e != nil || exists != s.existed || exists && sha256.Sum256(raw) != s.digest {
		return fail()
	}
	if len(s.data.Incidents) > MaxIncidents || len(s.data.Pending) > MaxPending || len(s.data.Holds) > 65 {
		return fail()
	}
	raw, e = json.Marshal(s.data)
	if e != nil || len(raw) > maxBytes {
		return fail()
	}
	f, e := os.CreateTemp(filepath.Dir(s.file), ".monitoring-")
	if e != nil {
		return fail()
	}
	defer os.Remove(f.Name())
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return fail()
	}
	if os.Rename(f.Name(), s.file) != nil {
		return fail()
	}
	s.digest = sha256.Sum256(raw)
	s.existed = true
	dir, e := os.Open(filepath.Dir(s.file))
	if e != nil {
		return fail()
	}
	e = dir.Sync()
	dir.Close()
	if e != nil {
		return fail()
	}
	s.persistenceError = false
	return nil
}
