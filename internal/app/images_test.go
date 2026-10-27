package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var oldImage = "sha256:" + strings.Repeat("a", 64)
var newImage = "sha256:" + strings.Repeat("b", 64)
var oldContainer = strings.Repeat("c", 64)
var newContainer = strings.Repeat("d", 64)

type imageExecutor struct {
	mu             sync.Mutex
	calls          []string
	after          bool
	tags           []string
	used           bool
	stopped        bool
	health         string
	unchanged      bool
	fail           string
	imageRef       string
	build          bool
	registryID     string
	noContainers   bool
	captureFailure bool
	changedProject bool
	gate           chan struct{}
	entered        chan struct{}
}

func (x *imageExecutor) Run(ctx context.Context, p Project, args []string, w io.Writer) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	command := strings.Join(args, " ")
	x.calls = append(x.calls, command)
	if x.fail != "" && strings.Contains(command, x.fail) {
		io.WriteString(w, "private-registry-password")
		return errors.New("private-registry-password")
	}
	emit := func(v any) error { return json.NewEncoder(w).Encode(v) }
	switch {
	case strings.HasSuffix(command, "config --format json"):
		ref := x.imageRef
		if ref == "" {
			ref = "example.test/app:latest"
		}
		service := map[string]any{"image": ref}
		if x.build {
			service["build"] = map[string]string{"context": "."}
		}
		return emit(map[string]any{"services": map[string]any{"web": service}})
	case strings.HasSuffix(command, "ps --all --quiet"):
		if x.captureFailure {
			return errors.New("capture failed")
		}
		if x.noContainers {
			return nil
		}
		id := oldContainer
		if x.after {
			id = newContainer
		}
		_, e := io.WriteString(w, id)
		return e
	case len(args) > 1 && args[0] == "container" && args[1] == "inspect":
		id := oldImage
		container := oldContainer
		if x.after {
			container = newContainer
			if !x.unchanged {
				id = newImage
			}
		}
		project := p.ProjectName
		if x.changedProject {
			project = "someone-else"
		}
		return emit(containerImage{ID: container, ImageID: id, Service: "web", Project: project, Running: !x.stopped, Health: x.health})
	case len(args) > 1 && args[0] == "image" && args[1] == "inspect":
		return emit(localImage{ID: args[len(args)-1], imagePlatform: imagePlatform{OS: "linux", Architecture: "amd64"}, RepoTags: x.tags})
	case strings.HasPrefix(command, "manifest inspect --verbose "):
		if x.entered != nil {
			close(x.entered)
			x.entered = nil
			select {
			case <-x.gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		id := x.registryID
		if id == "" {
			id = newImage
		}
		return emit([]any{map[string]any{"Descriptor": map[string]any{"platform": map[string]string{"os": "linux", "architecture": "amd64"}}, "OCIManifest": map[string]any{"config": map[string]string{"digest": id}}}})
	case strings.HasPrefix(command, "ps --all --quiet --no-trunc --filter ancestor="):
		if x.used {
			_, err := io.WriteString(w, oldContainer)
			return err
		}
		return nil
	case strings.HasPrefix(command, "image rm --no-prune "):
		return nil
	case strings.HasSuffix(command, "up -d"):
		x.after = true
		return nil
	default:
		return nil
	}
}
func (x *imageExecutor) commands() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return strings.Join(x.calls, "\n")
}
func TestImageCheckUsesRegistryContentAndNeverPulls(t *testing.T) {
	for _, tc := range []struct {
		name   string
		x      *imageExecutor
		status string
	}{
		{"new config digest", &imageExecutor{}, "available"},
		{"same config digest", &imageExecutor{registryID: oldImage}, "current"},
		{"registry authentication error", &imageExecutor{fail: "manifest inspect"}, "unknown"},
		{"digest pinned", &imageExecutor{imageRef: "example.test/app@" + oldImage}, "pinned"},
		{"local build", &imageExecutor{build: true}, "local"},
		{"missing deployment", &imageExecutor{noContainers: true}, "not_deployed"},
		{"invalid reference", &imageExecutor{imageRef: "--insecure private-registry-password"}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ImageService{Exec: tc.x}
			rows, err := s.check(context.Background(), Project{ProjectName: "fixture"})
			if err != nil || len(rows) != 1 || rows[0].Status != tc.status {
				t.Fatalf("%+v %v", rows, err)
			}
			raw, _ := json.Marshal(rows)
			if bytes.Contains(raw, []byte("private-registry-password")) {
				t.Fatal("registry secret leaked")
			}
			commands := tc.x.commands()
			for _, forbidden := range []string{" pull", " up ", "image rm", "prune"} {
				if strings.Contains(commands, forbidden) {
					t.Fatal(commands)
				}
			}
			if tc.status == "local" || tc.status == "pinned" || tc.status == "not_deployed" {
				if strings.Contains(commands, "manifest inspect") {
					t.Fatal("unnecessary registry request")
				}
			}
		})
	}
}
func TestRemoteImageIDPlatformAndFormats(t *testing.T) {
	raw := `[{"Descriptor":{"platform":{"os":"linux","architecture":"amd64"}},"SchemaV2Manifest":{"config":{"digest":"` + oldImage + `"}}},{"Descriptor":{"platform":{"os":"linux","architecture":"arm64","variant":"v8"}},"OCIManifest":{"config":{"digest":"` + newImage + `"}}},{"Descriptor":{"platform":{"os":"unknown","architecture":"unknown"}},"OCIManifest":{"config":{"digest":"` + oldImage + `"}}}]`
	var manifests []registryManifest
	if json.Unmarshal([]byte(raw), &manifests) != nil {
		t.Fatal("fixture")
	}
	for _, tc := range []struct {
		platform imagePlatform
		want     string
	}{{imagePlatform{OS: "linux", Architecture: "amd64"}, oldImage}, {imagePlatform{OS: "linux", Architecture: "arm64"}, newImage}} {
		got, err := remoteImageID(manifests, tc.platform)
		if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
	if _, err := remoteImageID(manifests, imagePlatform{OS: "linux", Architecture: "arm", Variant: "v7"}); err == nil {
		t.Fatal("unsupported platform accepted")
	}
	manifests = append(manifests, manifests[0])
	manifests[len(manifests)-1].SchemaV2Manifest = nil
	manifests[len(manifests)-1].OCIManifest = manifests[1].OCIManifest
	if _, err := remoteImageID(manifests, imagePlatform{OS: "linux", Architecture: "amd64"}); err == nil {
		t.Fatal("ambiguous manifests accepted")
	}
	var legacy registryManifest
	json.Unmarshal([]byte(`{"Platform":{"os":"linux","architecture":"amd64"},"SchemaV2Manifest":{"config":{"digest":"`+oldImage+`"}}}`), &legacy)
	if got, err := remoteImageID([]registryManifest{legacy}, imagePlatform{OS: "linux", Architecture: "amd64"}); err != nil || got != oldImage {
		t.Fatal("legacy format", got, err)
	}
}
func TestImageManifestCacheAndOutputLimit(t *testing.T) {
	x := &imageExecutor{}
	s := &ImageService{Exec: x}
	for i := 0; i < 2; i++ {
		if _, err := s.manifests(context.Background(), Project{}, "example.test/app:latest"); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Count(x.commands(), "manifest inspect") != 1 {
		t.Fatal(x.commands())
	}
	var output imageOutput
	if _, err := output.Write(make([]byte, 4<<20)); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte{0}); err == nil {
		t.Fatal("unbounded output")
	}
}
func TestCleanupOnlyRemovesReplacedUnusedUntaggedImages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		x      *imageExecutor
		remove bool
	}{
		{"replaced unused dangling", &imageExecutor{after: true}, true},
		{"tagged image retained", &imageExecutor{after: true, tags: []string{"app:old"}}, false},
		{"stopped or running other container", &imageExecutor{after: true, used: true}, false},
		{"new container stopped", &imageExecutor{after: true, stopped: true}, false},
		{"unhealthy update", &imageExecutor{after: true, health: "unhealthy"}, false},
		{"unchanged image", &imageExecutor{after: true, unchanged: true}, false},
		{"failed verification", &imageExecutor{after: true, fail: "container inspect"}, false},
		{"project identity mismatch", &imageExecutor{after: true, changedProject: true}, false},
		{"no replacement", &imageExecutor{after: true, noContainers: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var log bytes.Buffer
			s := &ImageService{Exec: tc.x}
			s.cleanup(context.Background(), Project{ProjectName: "fixture"}, []containerImage{{Service: "web", ImageID: oldImage}}, &log)
			commands := tc.x.commands()
			removed := strings.Contains(commands, "image rm --no-prune "+oldImage)
			if removed != tc.remove {
				t.Fatal(commands, log.String())
			}
			for _, bad := range []string{"--force", "image prune", "system prune", "volume", newImage} {
				if strings.Contains(commands, bad) {
					t.Fatal(commands)
				}
			}
		})
	}
}
func TestCleanupHealthWaitCancellation(t *testing.T) {
	x := &imageExecutor{after: true, health: "starting"}
	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	(&ImageService{Exec: x}).cleanup(ctx, Project{ProjectName: "fixture"}, []containerImage{{Service: "web", ImageID: oldImage}}, &log)
	if strings.Contains(x.commands(), "image rm") || !strings.Contains(log.String(), "timed out") {
		t.Fatal(log.String())
	}
}
func TestApplyImageCleanupLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name           string
		fail           string
		captureFailure bool
		expectRemoval  bool
	}{
		{name: "successful apply", expectRemoval: true}, {name: "failed pull", fail: " pull"}, {name: "failed up", fail: " up -d"}, {name: "failed snapshot", captureFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, p, rev := fixture(t, &fakeExecutor{})
			x := &imageExecutor{fail: tc.fail, captureFailure: tc.captureFailure}
			m.Exec = x
			m.Images = &ImageService{Exec: x}
			launch(t, m, p, rev, "apply")
			calls := x.commands()
			removed := strings.Contains(calls, "image rm --no-prune "+oldImage)
			if removed != tc.expectRemoval {
				t.Fatal(calls)
			}
			if removed && strings.Index(calls, " up -d") > strings.Index(calls, "image rm") {
				t.Fatal("cleanup before recreation")
			}
			if tc.captureFailure && m.Projects[p.ID].Op.Phase != "completed" {
				t.Fatal("optional cleanup failure broke update")
			}
		})
	}
}
func TestImageCheckLockRevisionAndAuth(t *testing.T) {
	m, p, rev := fixture(t, &fakeExecutor{})
	gate := make(chan struct{})
	entered := make(chan struct{})
	x := &imageExecutor{gate: gate, entered: entered}
	m.Images = &ImageService{Exec: x}
	if err := m.checkImages(p.ID, false); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := m.begin(p.ID, "stop", rev, ""); err == nil {
		t.Fatal("operation overlapped check")
	}
	if err := m.checkImages(p.ID, false); err == nil {
		t.Fatal("duplicate check")
	}
	if err := os.WriteFile(filepath.Join(p.Directory, "compose.yaml"), []byte("services: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	close(gate)
	m.wg.Wait()
	report := m.Projects[p.ID].ImageUpdates
	if report.Error == "" || report.Revision != "" {
		t.Fatal("outdated check accepted", report)
	}
	s := newServer(m, Config{}, "long-password-123456")
	s.sessions[sessionKey("cookie")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	h := s.Handler()
	cookie := &http.Cookie{Name: "qcu_session", Value: "cookie"}
	body := url.Values{"id": {p.ID}, "csrf": {"csrf"}}.Encode()
	for _, tc := range []struct {
		method, path, body, origin string
		cookie                     *http.Cookie
		status                     int
	}{
		{"POST", "/api/image-updates", body, "http://reef.test", nil, 401},
		{"POST", "/api/image-updates", body, "http://evil.test", cookie, 403},
		{"POST", "/api/image-updates", "id=test&csrf=wrong", "http://reef.test", cookie, 403},
		{"GET", "/api/image-updates", "", "", cookie, 405},
		{"POST", "/api/image-updates?host=remote", body, "http://reef.test", cookie, 409},
		{"POST", "/api/image-updates", "id=missing&csrf=csrf", "http://reef.test", cookie, 409},
		{"POST", "/api/image-updates", body, "http://reef.test", cookie, 202},
	} {
		if w := call(h, tc.method, tc.path, tc.body, tc.origin, tc.cookie); w.Code != tc.status {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	m.wg.Wait()
}
func TestAutomaticImageChecksStopAndDoNotRecheckRecentReports(t *testing.T) {
	m, p, _ := fixture(t, &fakeExecutor{})
	x := &imageExecutor{}
	m.Images = &ImageService{Exec: x}
	m.Projects[p.ID].ImageUpdates.CheckedAt = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	m.startImageChecks(ctx)
	cancel()
	m.wg.Wait()
	if x.commands() != "" {
		t.Fatal("recent/cancelled automatic check ran")
	}
}

func TestAutomaticInitialImageCheck(t *testing.T) {
	m, p, _ := fixture(t, &fakeExecutor{})
	x := &imageExecutor{}
	m.Images = &ImageService{Exec: x}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.startImageChecks(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for {
		mp, _ := m.get(p.ID)
		mp.mu.Lock()
		checked := !mp.ImageUpdates.CheckedAt.IsZero()
		mp.mu.Unlock()
		if checked {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			m.wg.Wait()
			t.Fatal("initial automatic check did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	m.wg.Wait()
	if strings.Count(x.commands(), "manifest inspect") != 1 {
		t.Fatal("initial check missing", x.commands())
	}
	if err := m.checkImages(p.ID, false); err == nil {
		t.Fatal("check started after shutdown")
	}
}
