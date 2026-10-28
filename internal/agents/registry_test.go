package agents

import (
	"os"
	"path/filepath"
	"reef/internal/docker"
	"strings"
	"sync"
	"testing"
	"time"
)

func enrolled(t *testing.T, r *Registry, name string) (string, string) {
	t.Helper()
	token, e := r.Token(name)
	if e != nil {
		t.Fatal(e)
	}
	id, key, e := r.Enroll(token, Protocol)
	if e != nil {
		t.Fatal(e)
	}
	return id, key
}
func TestEnrollmentSingleUseAndDurableRevocation(t *testing.T) {
	dir := t.TempDir()
	r, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	token, e := r.Token("NAS")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	var id, key string
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, b, e := r.Enroll(token, Protocol)
			if e == nil {
				mu.Lock()
				success++
				id, key = a, b
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatal(success)
	}
	b, e := os.ReadFile(filepath.Join(dir, "agents", "registry.json"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), token) || strings.Contains(string(b), key) {
		t.Fatal("raw secret persisted")
	}
	r, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Begin(id, key, "test", Protocol); e != nil {
		t.Fatal(e)
	}
	if e = r.Change(id, "", true); e != nil {
		t.Fatal(e)
	}
	r, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Begin(id, key, "test", Protocol); e == nil {
		t.Fatal("revocation lost")
	}
}
func TestIsolationGenerationAndStaleInventory(t *testing.T) {
	r, _ := Open("")
	a, ak := enrolled(t, r, "A")
	b, bk := enrolled(t, r, "B")
	ag, _ := r.Begin(a, ak, "v1", Protocol)
	bg, _ := r.Begin(b, bk, "v1", Protocol)
	row := docker.Container{ID: strings.Repeat("a", 64), Name: "same", State: "running", ComposeProject: "same"}
	u := Update{Protocol: Protocol, Generation: ag, Sequence: 1, Containers: []docker.Container{row}}
	if e := r.Publish(a, ak, u); e != nil {
		t.Fatal(e)
	}
	if e := r.Publish(b, bk, u); e == nil {
		t.Fatal("foreign generation accepted")
	}
	u.Generation = bg
	u.Containers[0].Name = "B"
	if e := r.Publish(b, bk, u); e != nil {
		t.Fatal(e)
	}
	view, rows, _ := r.Inventory(a)
	if view.Stale || rows[0].Name != "same" {
		t.Fatal(view, rows)
	}
	if e := r.Publish(a, ak, Update{Protocol: Protocol, Generation: ag, Sequence: 1}); e == nil {
		t.Fatal("reordered snapshot accepted")
	}
	next, _ := r.Begin(a, ak, "v1", Protocol)
	if next == ag {
		t.Fatal("generation reused")
	}
	u.Generation = ag
	u.Sequence = 2
	if e := r.Publish(a, ak, u); e == nil {
		t.Fatal("old generation accepted")
	}
	if e := r.Publish(a, ak, Update{Protocol: Protocol, Generation: next, Sequence: 1, Error: "Docker unavailable"}); e != nil {
		t.Fatal(e)
	}
	view, rows, _ = r.Inventory(a)
	if !view.Stale || len(rows) != 1 || rows[0].Name != "same" {
		t.Fatal(view, rows)
	}
	v := r.live[a]
	v.Contact = time.Now().Add(-time.Minute)
	r.live[a] = v
	view, _, _ = r.Inventory(a)
	if view.Status != "disconnected" {
		t.Fatal(view)
	}
}
func TestValidationAndFailedPersistence(t *testing.T) {
	r, _ := Open("")
	token, _ := r.Token("expired")
	r.data.Tokens[hash(token)] = enrollment{"expired", time.Now().Add(-time.Second)}
	if _, _, e := r.Enroll(token, Protocol); e == nil {
		t.Fatal("expired enrollment accepted")
	}
	a, key := enrolled(t, r, "A")
	generation, _ := r.Begin(a, key, "v1", Protocol)
	if _, e := r.Begin(a, "wrong", "v1", Protocol); e == nil {
		t.Fatal("bad credential accepted")
	}
	if _, e := r.Begin(a, key, "v1", 99); e == nil {
		t.Fatal("bad protocol accepted")
	}
	if e := r.Publish(a, key, Update{Protocol: Protocol, Generation: generation, Sequence: 1, Containers: []docker.Container{{ID: "bad", State: "running"}}}); e == nil {
		t.Fatal("bad inventory accepted")
	}
	if e := r.Publish(a, key, Update{Protocol: Protocol, Generation: generation, Sequence: 1, Containers: make([]docker.Container, 10001)}); e == nil {
		t.Fatal("oversized inventory accepted")
	}
	dir := t.TempDir()
	r, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	a, key = enrolled(t, r, "A")
	r.path = filepath.Join(dir, "missing", "registry.json")
	if e = r.Change(a, "", true); e == nil {
		t.Fatal("storage failure hidden")
	}
	if _, e = r.Begin(a, key, "v1", Protocol); e != nil {
		t.Fatal("failed transaction revoked session")
	}
}
func TestUnsafeRegistryRejected(t *testing.T) {
	dir := t.TempDir()
	r, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	enrolled(t, r, "A")
	path := filepath.Join(dir, "agents", "registry.json")
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(dir); e == nil {
		t.Fatal("public registry accepted")
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(filepath.Join(dir, "elsewhere"), path); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(dir); e == nil {
		t.Fatal("symlink accepted")
	}
}

func TestImageCapabilityBelongsToCurrentConnection(t *testing.T) {
	r, _ := Open(t.TempDir())
	id, key := enrolled(t, r, "images")
	gen, _ := r.Begin(id, key, "test", Protocol)
	if err := r.Images(id, gen, true); err != nil || r.CanImages(id) {
		t.Fatal("images requires control", err)
	}
	r.Control(id, gen, true)
	r.Images(id, gen, true)
	if !r.CanImages(id) {
		t.Fatal("missing image capability")
	}
	views := r.Views()
	found := false
	for _, cap := range views[0].Capabilities {
		found = found || cap == ImagesCapability
	}
	if !found {
		t.Fatal(views)
	}
	next, _ := r.Begin(id, key, "test", Protocol)
	r.Control(id, next, true)
	if r.CanImages(id) {
		t.Fatal("capability survived reconnect")
	}
	if err := r.Images(id, gen, true); err == nil {
		t.Fatal("stale generation accepted")
	}
	r.Images(id, next, true)
	r.Change(id, "", true)
	if r.CanImages(id) {
		t.Fatal("revoked host retained capability")
	}
}

func TestVolumeCapabilityBelongsToCurrentConnection(t *testing.T) {
	r, _ := Open(t.TempDir())
	id, key := enrolled(t, r, "volumes")
	gen, _ := r.Begin(id, key, "test", Protocol)
	if err := r.Volumes(id, gen, true); err != nil || r.CanVolumes(id) {
		t.Fatal("volumes requires control", err)
	}
	r.Control(id, gen, true)
	r.Volumes(id, gen, true)
	if !r.CanVolumes(id) {
		t.Fatal("missing volume capability")
	}
	views := r.Views()
	found := false
	for _, cap := range views[0].Capabilities {
		found = found || cap == VolumesCapability
	}
	if !found {
		t.Fatal(views)
	}
	next, _ := r.Begin(id, key, "test", Protocol)
	r.Control(id, next, true)
	if r.CanVolumes(id) {
		t.Fatal("capability survived reconnect")
	}
	if err := r.Volumes(id, gen, true); err == nil {
		t.Fatal("stale generation accepted")
	}
	r.Volumes(id, next, true)
	r.Change(id, "", true)
	if r.CanVolumes(id) {
		t.Fatal("revoked host retained capability")
	}
}

func TestImageBatchCapabilityBelongsToCurrentConnection(t *testing.T) {
	r, _ := Open(t.TempDir())
	id, key := enrolled(t, r, "images")
	gen, _ := r.Begin(id, key, "test", Protocol)
	if err := r.ImageBatch(id, gen, true); err != nil || r.CanImageBatch(id) {
		t.Fatal("images requires control", err)
	}
	r.Control(id, gen, true)
	r.Images(id, gen, true)
	r.ImageBatch(id, gen, true)
	if !r.CanImageBatch(id) {
		t.Fatal("missing image capability")
	}
	views := r.Views()
	found := false
	for _, cap := range views[0].Capabilities {
		found = found || cap == ImageBatchCapability
	}
	if !found {
		t.Fatal(views)
	}
	next, _ := r.Begin(id, key, "test", Protocol)
	r.Control(id, next, true)
	r.Images(id, next, true)
	if r.CanImageBatch(id) {
		t.Fatal("capability survived reconnect")
	}
	if err := r.ImageBatch(id, gen, true); err == nil {
		t.Fatal("stale generation accepted")
	}
	r.ImageBatch(id, next, true)
	r.Change(id, "", true)
	if r.CanImageBatch(id) {
		t.Fatal("revoked host retained capability")
	}
}

func TestBuildCapabilityBelongsToCurrentConnection(t *testing.T) {
	r, _ := Open(t.TempDir())
	id, key := enrolled(t, r, "volumes")
	gen, _ := r.Begin(id, key, "test", Protocol)
	if err := r.Builds(id, gen, true); err != nil || r.CanBuilds(id) {
		t.Fatal("volumes requires control", err)
	}
	r.Control(id, gen, true)
	r.Builds(id, gen, true)
	if !r.CanBuilds(id) {
		t.Fatal("missing volume capability")
	}
	views := r.Views()
	found := false
	for _, cap := range views[0].Capabilities {
		found = found || cap == BuildsCapability
	}
	if !found {
		t.Fatal(views)
	}
	next, _ := r.Begin(id, key, "test", Protocol)
	r.Control(id, next, true)
	if r.CanBuilds(id) {
		t.Fatal("capability survived reconnect")
	}
	if err := r.Builds(id, gen, true); err == nil {
		t.Fatal("stale generation accepted")
	}
	r.Builds(id, next, true)
	r.Change(id, "", true)
	if r.CanBuilds(id) {
		t.Fatal("revoked host retained capability")
	}
}
