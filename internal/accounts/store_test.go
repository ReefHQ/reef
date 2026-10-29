package accounts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const password = "a-strong-test-password"

func TestBootstrapPersistencePasswordsAndLastAdmin(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir, password)
	if e != nil {
		t.Fatal(e)
	}
	admin, ok := s.Authenticate("admin", password)
	if !ok || !admin.GlobalAdmin() {
		t.Fatal(admin)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "auth", "users.json"))
	if strings.Contains(string(b), password) {
		t.Fatal("plaintext password")
	}
	u, e := s.Change(User{Username: "reader", Role: "viewer", Hosts: []string{"local"}}, password, false)
	if e != nil || !u.Allows("local") || u.Allows(strings.Repeat("a", 64)) {
		t.Fatal(u, e)
	}
	if _, ok = s.Authenticate("reader", "wrong"); ok {
		t.Fatal("wrong password")
	}
	if _, ok = s.Authenticate("missing", password); ok {
		t.Fatal("unknown user")
	}
	admin.Disabled = true
	if _, e = s.Change(admin, "", false); e == nil {
		t.Fatal("last admin disabled")
	}
	admin.Disabled = false
	if _, e = s.Change(admin, "", true); e == nil {
		t.Fatal("last admin removed")
	}
	admin.AllHosts = false
	admin.Hosts = []string{"local"}
	if _, e = s.Change(admin, "", false); e == nil {
		t.Fatal("last all-host admin narrowed")
	}
	restarted, e := Open(dir, "different-bootstrap-secret")
	if e != nil {
		t.Fatal(e)
	}
	if _, ok = restarted.Authenticate("admin", password); !ok {
		t.Fatal("bootstrap secret silently reset established account")
	}
	if _, ok = restarted.Authenticate("admin", "different-bootstrap-secret"); ok {
		t.Fatal("backdoor bootstrap login")
	}
	if e = restarted.ChangePassword(u.ID, password, "a-new-strong-password"); e != nil {
		t.Fatal(e)
	}
	changed, _ := restarted.User(u.ID)
	if changed.Revision == u.Revision {
		t.Fatal("password did not revoke old revision")
	}
	if _, e = restarted.Change(u, "", false); e == nil {
		t.Fatal("stale update accepted")
	}
	if _, ok = restarted.Authenticate("reader", password); ok {
		t.Fatal("old password")
	}
	if _, ok = restarted.Authenticate("reader", "a-new-strong-password"); !ok {
		t.Fatal("new password")
	}
	if e = restarted.Recover("reader", password); e != nil {
		t.Fatal(e)
	}
	recovered, _ := restarted.User(u.ID)
	if !recovered.GlobalAdmin() || recovered.Revision <= changed.Revision {
		t.Fatal(recovered)
	}
}
func TestPrivateCorruptChangedAndMissingEstablishedAccountState(t *testing.T) {
	for _, mode := range []string{"corrupt", "public", "symlink", "missing", "external"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			s, e := Open(dir, password)
			if e != nil {
				t.Fatal(e)
			}
			file := filepath.Join(dir, "auth", "users.json")
			switch mode {
			case "corrupt":
				os.WriteFile(file, []byte("{"), 0600)
			case "public":
				os.Chmod(file, 0644)
			case "symlink":
				os.Rename(file, file+".original")
				os.Symlink(file+".original", file)
			case "missing":
				os.Remove(file)
			case "external":
				os.WriteFile(file, []byte("{}"), 0600)
			}
			admin := s.List()[0]
			if _, e = s.Change(admin, "", false); e == nil {
				t.Fatal("unsafe state changed")
			}
			if _, e = Open(dir, password); e == nil {
				t.Fatal("unsafe state accepted or lost users silently bootstrapped")
			}
		})
	}
}
func TestCommitTimeActorPermissionsAndSelfServiceCannotEscalate(t *testing.T) {
	s, e := Open(t.TempDir(), password)
	if e != nil {
		t.Fatal(e)
	}
	admin := s.List()[0]
	viewer, e := s.Change(User{Username: "reader", Role: "viewer", Hosts: []string{"local"}}, password, false)
	if e != nil {
		t.Fatal(e)
	}
	elevated := viewer
	elevated.Role = "admin"
	elevated.AllHosts = true
	if _, e = s.ChangeBy(viewer, elevated, "", false, false); e == nil {
		t.Fatal("self-service escalated")
	}
	if _, e = s.ChangeBy(viewer, admin, "", false, true); e == nil {
		t.Fatal("viewer administered accounts")
	}
	if _, e = s.ChangeBy(viewer, viewer, "", false, false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeBy(viewer, viewer, "", false, false); e == nil {
		t.Fatal("stale actor admitted")
	}
	if _, e = s.Change(User{Username: "bad name", Role: "viewer"}, password, false); e == nil {
		t.Fatal("invalid user")
	}
	if s.ChangePassword(viewer.ID, password, "") == nil {
		t.Fatal("empty password accepted")
	}
}
