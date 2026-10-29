// Package volumebackup stores host-local archives. Archive bytes never enter the
// agent relay or the browser. Only successfully validated archives are restorable.
package volumebackup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const MaxArchive int64 = 20 << 30
const MaxStorage int64 = 100 << 30

var idPattern = regexp.MustCompile(`^[a-f0-9]{48}$`)
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)
var ErrState = errors.New("Backup storage is unavailable or changed; inspect the private backup folder")

type Record struct {
	Destination string    `json:"destination,omitempty"`
	Source      string    `json:"source,omitempty"`
	ID          string    `json:"id"`
	Volume      string    `json:"volume"`
	Fingerprint string    `json:"fingerprint"`
	Created     time.Time `json:"created"`
	Status      string    `json:"status"`
	Bytes       int64     `json:"bytes"`
	SHA256      string    `json:"sha256,omitempty"`
	Containers  []string  `json:"containers"`
	Message     string    `json:"message"`
}
type Store struct {
	mu     sync.Mutex
	root   string
	keep   int
	hashes map[string]string
	loaded bool
	lock   *os.File
}

func (s *Store) Directory() string { return s.root }

func (s *Store) Close() error {
	if s.lock == nil {
		return nil
	}
	return s.lock.Close()
}

func private(p string) error {
	for q := p; ; q = filepath.Dir(q) {
		i, e := os.Lstat(q)
		if e == nil && i.Mode()&os.ModeSymlink != 0 {
			return ErrState
		}
		if e != nil && !os.IsNotExist(e) {
			return ErrState
		}
		if filepath.Dir(q) == q {
			break
		}
	}
	return nil
}
func Open(root string, keep int) (*Store, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) || keep < 1 || keep > 100 {
		return nil, ErrState
	}
	if private(root) != nil {
		return nil, ErrState
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	i, e := os.Lstat(root)
	if e != nil || !i.IsDir() || i.Mode().Perm()&0077 != 0 {
		return nil, ErrState
	}
	s := &Store{root: root, keep: keep, hashes: map[string]string{}}
	lp := filepath.Join(root, ".lock")
	if i, e := os.Lstat(lp); e == nil && (!i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0) {
		return nil, ErrState
	}
	lock, e := os.OpenFile(lp, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		return nil, errors.New("Backup directory is already in use by another Reef instance")
	}
	s.lock = lock
	ready := false
	defer func() {
		if !ready {
			lock.Close()
		}
	}()
	rows, e := s.read()
	if e != nil {
		return nil, e
	}
	for _, r := range rows {
		b, e := os.ReadFile(s.file(r.ID, ".json"))
		if e != nil {
			return nil, e
		}
		s.hashes[r.ID] = sum(b)
	}
	s.loaded = true
	ready = true
	return s, nil
}
func sum(b []byte) string                   { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (s *Store) file(id, ext string) string { return filepath.Join(s.root, id+ext) }
func valid(r Record) bool {
	if r.Destination != "" && (!namePattern.MatchString(r.Destination) || !idPattern.MatchString(r.Source)) {
		return false
	}
	if !idPattern.MatchString(r.ID) || !namePattern.MatchString(r.Volume) || len(r.Fingerprint) != 64 || r.Created.IsZero() || r.Bytes < 0 || r.Bytes > MaxArchive || len(r.Containers) > 100 || len(r.Message) > 1024 {
		return false
	}
	if _, e := hex.DecodeString(r.Fingerprint); e != nil {
		return false
	}
	for _, id := range r.Containers {
		if len(id) != 64 {
			return false
		}
		if _, e := hex.DecodeString(id); e != nil {
			return false
		}
	}
	switch r.Status {
	case "pending", "failed", "interrupted":
		return true
	case "succeeded":
		if r.Destination != "" {
			return r.Bytes == 0 && r.SHA256 == ""
		}
		b, e := hex.DecodeString(r.SHA256)
		return e == nil && len(b) == 32 && r.Bytes > 0
	}
	return false
}
func (s *Store) read() ([]Record, error) {
	if private(s.root) != nil {
		return nil, ErrState
	}
	if i, e := os.Lstat(s.root); e != nil || !i.IsDir() || i.Mode().Perm()&0077 != 0 {
		return nil, ErrState
	}
	files, e := os.ReadDir(s.root)
	if e != nil || len(files) > 30000 {
		return nil, ErrState
	}
	rows := []Record{}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".json")
		if !idPattern.MatchString(id) {
			return nil, ErrState
		}
		p := s.file(id, ".json")
		i, e := os.Lstat(p)
		if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 || i.Size() > 1<<20 {
			return nil, ErrState
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil, ErrState
		}
		if old, ok := s.hashes[id]; (ok && old != sum(b)) || (!ok && s.loaded) {
			return nil, ErrState
		}
		var r Record
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || r.ID != id || !valid(r) {
			return nil, ErrState
		}
		rows = append(rows, r)
	}
	if len(rows) > 10000 {
		return nil, ErrState
	}
	if s.loaded && len(rows) != len(s.hashes) {
		return nil, ErrState
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Created.After(rows[j].Created) })
	return rows, nil
}
func (s *Store) List() ([]Record, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.read() }
func (s *Store) Save(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !valid(r) {
		return ErrState
	}
	rows, e := s.read()
	if e != nil {
		return e
	}
	if _, exists := s.hashes[r.ID]; !exists && len(rows) >= 10000 {
		return errors.New("Backup catalog full; delete old records before continuing")
	}
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(s.root, ".metadata-")
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
	if e = os.Rename(f.Name(), s.file(r.ID, ".json")); e != nil {
		return e
	}
	s.hashes[r.ID] = sum(b)
	d, e := os.Open(s.root)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (s *Store) CreateArchive(id string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !idPattern.MatchString(id) {
		return nil, ErrState
	}
	rows, e := s.read()
	if e != nil {
		return nil, e
	}
	var total int64
	for _, r := range rows {
		total += r.Bytes
	}
	files, e := os.ReadDir(s.root)
	if e != nil {
		return nil, e
	}
	total = 0
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".tar.gz") || strings.HasSuffix(f.Name(), ".partial") {
			i, e := f.Info()
			if e != nil || !i.Mode().IsRegular() {
				return nil, ErrState
			}
			total += i.Size()
		}
	}
	if total > MaxStorage-MaxArchive {
		return nil, errors.New("Backup storage limit reached; free space before creating another archive")
	}
	return os.OpenFile(s.file(id, ".partial"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}
func (s *Store) Finish(id string) (int64, string, error) {
	if !idPattern.MatchString(id) {
		return 0, "", ErrState
	}
	p := s.file(id, ".partial")
	n, hash, e := Validate(p)
	if e != nil {
		return 0, "", e
	}
	if e = os.Rename(p, s.file(id, ".tar.gz")); e != nil {
		return 0, "", e
	}
	return n, hash, nil
}
func (s *Store) Archive(r Record) (string, error) {
	if !valid(r) || r.Status != "succeeded" || r.Destination != "" {
		return "", ErrState
	}
	p := s.file(r.ID, ".tar.gz")
	n, h, e := Validate(p)
	if e != nil || n != r.Bytes || h != r.SHA256 {
		return "", errors.New("Backup archive is missing, unsafe or failed its checksum")
	}
	return p, nil
}
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !idPattern.MatchString(id) {
		return ErrState
	}
	rows, e := s.read()
	if e != nil {
		return e
	}
	found := false
	for _, r := range rows {
		if r.ID == id {
			found = true
			if r.Status == "pending" {
				return errors.New("Cannot delete an active backup")
			}
		}
	}
	if !found {
		return ErrState
	}
	// Remove data before its catalog record, so a failure never leaves an invisible archive.
	for _, ext := range []string{".tar.gz", ".partial", ".json"} {
		if e = os.Remove(s.file(id, ext)); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	delete(s.hashes, id)
	return nil
}
func (s *Store) Retain(volume, fingerprint string) error {
	rows, e := s.List()
	if e != nil {
		return e
	}
	n := 0
	for _, r := range rows {
		if r.Volume == volume && r.Fingerprint == fingerprint && r.Status == "succeeded" && r.Destination == "" {
			n++
			if n > s.keep {
				if e = s.Delete(r.ID); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

// Validate rejects extraction through symlink ancestors, escaping hard links,
// duplicate paths, devices, sockets and decompression bombs. Absolute symlink
// targets are allowed as data, but no later member may be written through them.
func Validate(p string) (int64, string, error) {
	i, e := os.Lstat(p)
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 || i.Size() == 0 || i.Size() > MaxArchive {
		return 0, "", ErrState
	}
	f, e := os.Open(p)
	if e != nil {
		return 0, "", e
	}
	defer f.Close()
	h := sha256.New()
	z, e := gzip.NewReader(io.TeeReader(f, h))
	if e != nil {
		return 0, "", e
	}
	defer z.Close()
	t := tar.NewReader(io.LimitReader(z, 100<<30+1))
	seen := map[string]byte{}
	var total int64
	for {
		v, e := t.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return 0, "", errors.New("Incomplete backup archive")
		}
		n := path.Clean(v.Name)
		if path.IsAbs(v.Name) || n == ".." || strings.HasPrefix(n, "../") || strings.Contains(v.Name, "\x00") || len(n) > 4096 || len(seen) >= 1000000 {
			return 0, "", errors.New("Unsafe backup archive path")
		}
		if _, ok := seen[n]; ok {
			return 0, "", errors.New("Duplicate archive path")
		}
		for q := path.Dir(n); q != "." && q != "/"; q = path.Dir(q) {
			if typ, ok := seen[q]; ok && typ != tar.TypeDir {
				return 0, "", errors.New("Unsafe archive ancestor")
			}
		}
		switch v.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			total += v.Size
			if v.Size < 0 || total > 100<<30 {
				return 0, "", errors.New("Expanded archive exceeds 100 GiB")
			}
		case tar.TypeDir:
		case tar.TypeSymlink:
			if len(v.Linkname) > 4096 {
				return 0, "", ErrState
			}
			for old := range seen {
				if strings.HasPrefix(old, n+"/") {
					return 0, "", ErrState
				}
			}
		case tar.TypeLink:
			target := path.Clean(v.Linkname)
			typ, ok := seen[target]
			if path.IsAbs(v.Linkname) || !ok || (typ != tar.TypeReg && typ != tar.TypeRegA) {
				return 0, "", errors.New("Unsafe archive hard link")
			}
		default:
			return 0, "", errors.New("Unsupported file type in backup")
		}
		seen[n] = v.Typeflag
	}
	// Consume the gzip trailer and enforce integrity, including trailing compressed data.
	padding, e := io.ReadAll(io.LimitReader(z, 1<<20+1))
	if e != nil || len(padding) > 1<<20 || len(bytes.Trim(padding, "\x00")) != 0 {
		return 0, "", errors.New("Backup compression checksum failed")
	}
	if _, e = io.Copy(h, f); e != nil {
		return 0, "", e
	}
	return i.Size(), hex.EncodeToString(h.Sum(nil)), nil
}
