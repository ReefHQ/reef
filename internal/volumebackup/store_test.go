package volumebackup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testArchive(t *testing.T, headers ...*tar.Header) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	w := tar.NewWriter(z)
	for _, h := range headers {
		if e := w.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Size > 0 {
			if _, e := w.Write(bytes.Repeat([]byte("x"), int(h.Size))); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestArchiveValidation(t *testing.T) {
	regular := func(n string) *tar.Header {
		return &tar.Header{Name: n, Typeflag: tar.TypeReg, Size: 8, Mode: 0640, Uid: 1234, Gid: 1235}
	}
	cases := []struct {
		name    string
		headers []*tar.Header
		ok      bool
	}{
		{"regular", []*tar.Header{{Name: "./", Typeflag: tar.TypeDir, Mode: 0750}, regular("./data"), {Name: "./alias", Typeflag: tar.TypeLink, Linkname: "./data"}, {Name: "./external-link", Typeflag: tar.TypeSymlink, Linkname: "/var/lib/db"}}, true},
		{"escape", []*tar.Header{regular("../escape")}, false}, {"absolute", []*tar.Header{regular("/escape")}, false},
		{"symlink ancestor", []*tar.Header{{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp"}, regular("link/escape")}, false},
		{"replaced ancestor", []*tar.Header{regular("dir/file"), {Name: "dir", Typeflag: tar.TypeSymlink, Linkname: "/tmp"}}, false},
		{"hardlink escape", []*tar.Header{{Name: "alias", Typeflag: tar.TypeLink, Linkname: "../target"}}, false},
		{"duplicate", []*tar.Header{regular("data"), regular("./data")}, false},
		{"device", []*tar.Header{{Name: "device", Typeflag: tar.TypeChar, Mode: 0600}}, false},
		{"file ancestor", []*tar.Header{regular("data"), regular("data/escape")}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "archive")
			b := testArchive(t, c.headers...)
			if e := os.WriteFile(p, b, 0600); e != nil {
				t.Fatal(e)
			}
			n, h, e := Validate(p)
			if (e == nil) != c.ok {
				t.Fatal(e)
			}
			if c.ok && (n != int64(len(b)) || h != sum(b)) {
				t.Fatal("checksum mismatch")
			}
		})
	}
	p := filepath.Join(t.TempDir(), "truncated")
	b := testArchive(t, regular("data"))
	os.WriteFile(p, b[:len(b)-6], 0600)
	if _, _, e := Validate(p); e == nil {
		t.Fatal("truncated gzip accepted")
	}
	b[len(b)-5] ^= 255
	os.WriteFile(p, b, 0600)
	if _, _, e := Validate(p); e == nil {
		t.Fatal("corrupt gzip accepted")
	}
}
func TestStoreRetentionIsolationAndTampering(t *testing.T) {
	root := filepath.Join(t.TempDir(), "backups")
	s, e := Open(root, 1)
	if e != nil {
		t.Fatal(e)
	}
	add := func(ch string, status string, when int) Record {
		t.Helper()
		r := Record{ID: strings.Repeat(ch, 48), Volume: "database", Fingerprint: strings.Repeat("a", 64), Created: time.Unix(int64(when), 0).UTC(), Status: "pending", Containers: []string{}}
		if e = s.Save(r); e != nil {
			t.Fatal(e)
		}
		f, e := s.CreateArchive(r.ID)
		if e != nil {
			t.Fatal(e)
		}
		f.Write(testArchive(t, &tar.Header{Name: "data", Typeflag: tar.TypeReg, Size: 3, Mode: 0640}))
		f.Sync()
		f.Close()
		if status == "succeeded" {
			r.Bytes, r.SHA256, e = s.Finish(r.ID)
			if e != nil {
				t.Fatal(e)
			}
		}
		r.Status = status
		if e = s.Save(r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	old := add("1", "succeeded", 1)
	latest := add("2", "succeeded", 2)
	failed := add("3", "failed", 3)
	if e = s.Retain(latest.Volume, latest.Fingerprint); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.List()
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	if _, e = os.Stat(s.file(old.ID, ".tar.gz")); !os.IsNotExist(e) {
		t.Fatal("old archive retained")
	}
	if _, e = os.Stat(s.file(failed.ID, ".partial")); e != nil {
		t.Fatal("failed archive silently pruned")
	}
	if _, e = s.Archive(latest); e != nil {
		t.Fatal(e)
	}
	p := s.file(latest.ID, ".tar.gz")
	os.WriteFile(p, []byte("broken"), 0600)
	if _, e = s.Archive(latest); e == nil {
		t.Fatal("corrupt data restorable")
	}
	b, _ := os.ReadFile(s.file(latest.ID, ".json"))
	os.WriteFile(s.file(latest.ID, ".json"), append(b, ' '), 0600)
	if _, e = s.List(); e == nil {
		t.Fatal("external modification accepted")
	}
}
func TestStoreRefusesUnsafeStateAndActiveDeletion(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "private")
	s, e := Open(root, 7)
	if e != nil {
		t.Fatal(e)
	}
	r := Record{ID: strings.Repeat("1", 48), Volume: "data", Fingerprint: strings.Repeat("a", 64), Created: time.Now(), Status: "pending", Containers: []string{strings.Repeat("b", 64)}}
	if e = s.Save(r); e != nil {
		t.Fatal(e)
	}
	if e = s.Delete(r.ID); e == nil {
		t.Fatal("active deleted")
	}
	// A second read catches injected or deleted metadata rather than adopting it.
	r.ID = strings.Repeat("2", 48)
	b, _ := json.Marshal(r)
	os.WriteFile(s.file(r.ID, ".json"), b, 0600)
	if _, e = s.List(); e == nil {
		t.Fatal("injected metadata adopted")
	}
	os.Remove(s.file(r.ID, ".json"))
	os.Remove(s.file(strings.Repeat("1", 48), ".json"))
	if _, e = s.List(); e == nil {
		t.Fatal("missing state accepted")
	}
	os.Symlink(root, filepath.Join(base, "link"))
	if _, e = Open(filepath.Join(base, "link"), 7); e == nil {
		t.Fatal("symlink accepted")
	}
	os.Chmod(root, 0755)
	if _, e = Open(root, 7); e == nil {
		t.Fatal("public root accepted")
	}
}
