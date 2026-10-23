package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultRootIsInstallationParentAndCandidatesCannotChangeIt(t *testing.T) {
	c, p := adoptionFixture(t)
	parent := c.AllowedProjectRoots[0]
	c.AllowedProjectRoots = nil
	c.InstallationDirectory = filepath.Join(parent, "reef")
	if e := os.Mkdir(c.InstallationDirectory, 0700); e != nil {
		t.Fatal(e)
	}
	if got := allowedRoots(c); !reflect.DeepEqual(got, []string{parent}) {
		t.Fatal(got)
	}
	m, e := newManager(c, &fakeExecutor{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.registerProject(p); e != nil {
		t.Fatal(e)
	}
	if got := allowedRoots(c); !reflect.DeepEqual(got, []string{parent}) {
		t.Fatal("adopted project expanded roots", got)
	}
	for _, dir := range []string{parent + "-sibling", filepath.Dir(parent), filepath.Join(parent, "..", "outside"), c.InstallationDirectory} {
		candidate := p
		candidate.Directory = dir
		candidate.ProjectName = "other-candidate"
		if _, e = m.registerProject(candidate); e == nil {
			t.Fatal("escape or self-installation accepted", dir)
		}
	}
	link := filepath.Join(parent, "escape")
	outside := canonicalTemp(t)
	os.Symlink(outside, link)
	candidate := p
	candidate.Directory = link
	candidate.ProjectName = "escape-candidate"
	if _, e = m.registerProject(candidate); e == nil {
		t.Fatal("symlink escape accepted")
	}
}
func TestExplicitRootPriorityAndUnknownInstallationHasNoCwdFallback(t *testing.T) {
	install := filepath.Join(canonicalTemp(t), "reef")
	os.Mkdir(install, 0700)
	for _, roots := range [][]string{{}, {"/custom/explicit"}} {
		c := Config{InstallationDirectory: install, AllowedProjectRoots: roots}
		if !reflect.DeepEqual(allowedRoots(c), roots) || projectAccess(c).Source != "explicit" {
			t.Fatal("explicit roots lost priority")
		}
	}
	// Previously configured candidate paths are not installation metadata.
	c := Config{Projects: []Project{{Directory: "/candidate/from/config"}}}
	if len(allowedRoots(c)) != 0 || projectAccess(c).Source != "installation-unknown" {
		t.Fatal("inferred installation from a project or cwd")
	}
	if e := authorizedDirectory(Config{InstallationDirectory: install, AllowedProjectRoots: []string{filepath.Dir(install)}}, install); e == nil {
		t.Fatal("installation itself accepted with explicit roots")
	}
}
func TestConfigInstallationLocationAndEnvironmentPriority(t *testing.T) {
	parent := canonicalTemp(t)
	install := filepath.Join(parent, "reef")
	os.Mkdir(install, 0700)
	other := filepath.Join(parent, "other-reef")
	os.Mkdir(other, 0700)
	path := filepath.Join(canonicalTemp(t), "config.json")
	write := func(c Config) {
		t.Helper()
		b, e := json.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("REEF_INSTALLATION_DIR", install)
	write(Config{StateDir: canonicalTemp(t)})
	c, e := loadConfig(path)
	if e != nil || c.InstallationDirectory != install || !reflect.DeepEqual(allowedRoots(c), []string{parent}) {
		t.Fatal(c, e)
	}
	write(Config{StateDir: canonicalTemp(t), InstallationDirectory: other})
	c, e = loadConfig(path)
	if e != nil || c.InstallationDirectory != other {
		t.Fatal("config did not override environment", c, e)
	}
	t.Setenv("REEF_INSTALLATION_DIR", "relative/unknown")
	write(Config{StateDir: canonicalTemp(t), AllowedProjectRoots: []string{}})
	c, e = loadConfig(path)
	if e != nil || len(allowedRoots(c)) != 0 || c.AllowedProjectRoots == nil {
		t.Fatal("explicit empty roots did not override default", c, e)
	}
	t.Setenv("REEF_INSTALLATION_DIR", "")
	write(Config{StateDir: canonicalTemp(t)})
	c, e = loadConfig(path)
	if e != nil || len(allowedRoots(c)) != 0 {
		t.Fatal("container cwd used as a default", e)
	}
	for _, bad := range []string{"relative/reef", "/reef", filepath.Join(parent, "missing")} {
		write(Config{StateDir: canonicalTemp(t), InstallationDirectory: bad})
		if _, e = loadConfig(path); e == nil {
			t.Fatal("unsafe or missing installation accepted", bad)
		}
	}
	link := filepath.Join(parent, "linked-reef")
	os.Symlink(install, link)
	write(Config{StateDir: canonicalTemp(t), InstallationDirectory: link})
	if _, e = loadConfig(path); e == nil {
		t.Fatal("noncanonical installation accepted")
	}
}
