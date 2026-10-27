package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// These checks read registry manifests; they never pull an image or run Compose up.
type ImageUpdate struct {
	Service string `json:"service"`
	Image   string `json:"image"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}
type ImageUpdates struct {
	Checking  bool          `json:"checking"`
	CheckedAt time.Time     `json:"checked_at"`
	Revision  string        `json:"revision,omitempty"`
	Error     string        `json:"error,omitempty"`
	Services  []ImageUpdate `json:"services"`
}
type imagePlatform struct{ OS, Architecture, Variant string }
type containerImage struct {
	ID      string `json:"id"`
	ImageID string `json:"image_id"`
	Service string `json:"service"`
	Project string `json:"project"`
	Running bool   `json:"running"`
	Health  string `json:"health"`
}
type localImage struct {
	ID string `json:"Id"`
	imagePlatform
	RepoTags []string
}
type registryManifest struct {
	Descriptor       struct{ Platform imagePlatform }
	Platform         imagePlatform // Older Docker clients.
	SchemaV2Manifest *struct{ Config struct{ Digest string } }
	OCIManifest      *struct{ Config struct{ Digest string } }
}
type manifestCache struct {
	At        time.Time
	Manifests []registryManifest
}
type ImageService struct {
	Exec  Executor
	mu    sync.Mutex
	cache map[string]manifestCache
}

var imageIDPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var containerIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var imageRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,511}$`)

// Bound command output, including Compose configuration which may contain secrets.
// Never include captured output or CLI errors in API responses or persisted logs.
type imageOutput struct{ data bytes.Buffer }

func (b *imageOutput) Write(p []byte) (int, error) {
	if b.data.Len()+len(p) > 4<<20 {
		return 0, errors.New("image command output exceeds limit")
	}
	return b.data.Write(p)
}
func (s *ImageService) command(ctx context.Context, p Project, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output imageOutput
	run := s.Exec.Run
	if structured, ok := s.Exec.(interface {
		Output(context.Context, Project, []string, io.Writer) error
	}); ok {
		run = structured.Output
	}
	if err := run(ctx, p, args, &output); err != nil {
		return nil, errors.New("Docker image command failed")
	}
	return output.data.Bytes(), nil
}

const containerImageFormat = `{"id":{{json .Id}},"image_id":{{json .Image}},"service":{{json (index .Config.Labels "com.docker.compose.service")}},"project":{{json (index .Config.Labels "com.docker.compose.project")}},"running":{{.State.Running}},"health":{{if index .State "Health"}}{{json (index .State "Health" "Status")}}{{else}}""{{end}}}`
const localImageFormat = `{"Id":{{json .Id}},"OS":{{json .Os}},"Architecture":{{json .Architecture}},"Variant":{{json (index . "Variant")}},"RepoTags":{{json (index . "RepoTags")}}}`

func (s *ImageService) containers(ctx context.Context, p Project) ([]containerImage, error) {
	raw, err := s.command(ctx, p, composeArgs(p, "", "ps", "--all", "--quiet")...)
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(raw))
	if len(ids) > 200 {
		return nil, errors.New("project has too many containers to check")
	}
	rows := []containerImage{}
	for _, id := range ids {
		if !containerIDPattern.MatchString(id) {
			return nil, errors.New("invalid project container identity")
		}
		raw, err = s.command(ctx, p, "container", "inspect", "--format", containerImageFormat, id)
		if err != nil {
			return nil, err
		}
		var row containerImage
		if json.Unmarshal(raw, &row) != nil || row.ID != id || !imageIDPattern.MatchString(row.ImageID) || row.Project != p.ProjectName || row.Service == "" {
			return nil, errors.New("project container identity changed")
		}
		rows = append(rows, row)
	}
	return rows, nil
}
func (s *ImageService) image(ctx context.Context, p Project, id string) (localImage, error) {
	var image localImage
	if !imageIDPattern.MatchString(id) {
		return image, errors.New("invalid image identity")
	}
	raw, err := s.command(ctx, p, "image", "inspect", "--format", localImageFormat, id)
	if err != nil {
		return image, err
	}
	if json.Unmarshal(raw, &image) != nil || image.ID != id || image.OS == "" || image.Architecture == "" {
		return image, errors.New("invalid image metadata")
	}
	return image, nil
}
func (s *ImageService) manifests(ctx context.Context, p Project, ref string) ([]registryManifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.cache[ref]; ok && time.Since(entry.At) < time.Minute {
		return entry.Manifests, nil
	}
	raw, err := s.command(ctx, p, "manifest", "inspect", "--verbose", ref)
	if err != nil {
		return nil, errors.New("Registry unavailable or authentication required")
	}
	var list []registryManifest
	if len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '[' {
		err = json.Unmarshal(raw, &list)
	} else {
		var item registryManifest
		err = json.Unmarshal(raw, &item)
		list = []registryManifest{item}
	}
	if err != nil || len(list) == 0 {
		return nil, errors.New("Registry returned an unsupported manifest")
	}
	if s.cache == nil || len(s.cache) > 512 {
		s.cache = map[string]manifestCache{}
	}
	s.cache[ref] = manifestCache{time.Now(), list}
	return list, nil
}
func remoteImageID(manifests []registryManifest, platform imagePlatform) (string, error) {
	matches := map[string]bool{}
	for _, m := range manifests {
		remote := m.Descriptor.Platform
		if remote.OS == "" {
			remote = m.Platform
		}
		// arm64/v8 and arm64 are equivalent; other variants require exact matches.
		canonical := func(p imagePlatform) imagePlatform {
			if p.Architecture == "arm64" && p.Variant == "v8" {
				p.Variant = ""
			}
			return p
		}
		if canonical(remote) != canonical(platform) {
			continue
		}
		id := ""
		if m.SchemaV2Manifest != nil {
			id = m.SchemaV2Manifest.Config.Digest
		}
		if m.OCIManifest != nil {
			id = m.OCIManifest.Config.Digest
		}
		if imageIDPattern.MatchString(id) {
			matches[id] = true
		}
	}
	if len(matches) != 1 {
		return "", errors.New("No unambiguous registry image for the deployed platform")
	}
	for id := range matches {
		return id, nil
	}
	panic("unreachable")
}
func (s *ImageService) check(ctx context.Context, p Project) ([]ImageUpdate, error) {
	raw, err := s.command(ctx, p, composeArgs(p, "", "config", "--format", "json")...)
	if err != nil {
		return nil, err
	}
	var config struct {
		Services map[string]struct {
			Image string
			Build json.RawMessage
		}
	}
	if json.Unmarshal(raw, &config) != nil || len(config.Services) > 100 {
		return nil, errors.New("Cannot read project image configuration")
	}
	containers, err := s.containers(ctx, p)
	if err != nil {
		return nil, err
	}
	services := make([]string, 0, len(config.Services))
	for name := range config.Services {
		services = append(services, name)
	}
	sort.Strings(services)
	result := make([]ImageUpdate, 0, len(services))
	for _, name := range services {
		service := config.Services[name]
		row := ImageUpdate{Service: name, Image: service.Image, Status: "current"}
		switch {
		case service.Image == "" || imageIDPattern.MatchString(service.Image) || len(service.Build) > 0 && string(service.Build) != "null":
			row.Status = "local"
			row.Message = "Locally built images are not checked"
		case !imageRefPattern.MatchString(service.Image):
			row.Status = "unknown"
			row.Image = ""
			row.Message = "Unsupported image reference"
		case strings.Contains(service.Image, "@"):
			row.Status = "pinned"
			row.Message = "Digest-pinned image; change Compose to update it"
		default:
			deployed := []containerImage{}
			for _, c := range containers {
				if c.Service == name {
					deployed = append(deployed, c)
				}
			}
			if len(deployed) == 0 {
				row.Status = "not_deployed"
				row.Message = "No deployed container to compare"
				break
			}
			manifests, e := s.manifests(ctx, p, service.Image)
			if e != nil {
				row.Status = "unknown"
				row.Message = e.Error()
				break
			}
			for _, c := range deployed {
				image, e := s.image(ctx, p, c.ImageID)
				if e != nil {
					row.Status = "unknown"
					row.Message = "Cannot read the deployed image"
					break
				}
				id, e := remoteImageID(manifests, image.imagePlatform)
				if e != nil {
					row.Status = "unknown"
					row.Message = e.Error()
					break
				}
				if id != c.ImageID {
					row.Status = "available"
				}
			}
		}
		result = append(result, row)
	}
	return result, nil
}

// Cleanup is limited to captured images replaced in the same service. It never
// prunes globally, forces removal, removes tags, or touches volumes/build caches.
func (s *ImageService) cleanup(ctx context.Context, p Project, before []containerImage, w io.Writer) {
	if len(before) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var after []containerImage
	for {
		var err error
		after, err = s.containers(ctx, p)
		if err != nil || len(after) == 0 {
			fmt.Fprintln(w, "Image cleanup skipped: cannot verify updated containers.")
			return
		}
		ready := true
		for _, c := range after {
			if !c.Running || c.Health == "unhealthy" {
				fmt.Fprintln(w, "Image cleanup skipped: updated containers are not running/healthy.")
				return
			}
			if c.Health != "" && c.Health != "healthy" {
				ready = false
			}
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(w, "Image cleanup skipped: health verification timed out.")
			return
		case <-time.After(2 * time.Second):
		}
	}
	candidates := map[string]bool{}
	for _, old := range before {
		replaced, stillUsed := false, false
		for _, c := range after {
			if c.ImageID == old.ImageID {
				stillUsed = true
			}
			if c.Service == old.Service && c.ImageID != old.ImageID {
				replaced = true
			}
		}
		if replaced && !stillUsed {
			candidates[old.ImageID] = true
		}
	}
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		image, err := s.image(ctx, p, id)
		if err != nil || len(image.RepoTags) != 0 {
			fmt.Fprintln(w, "Old image retained: it is tagged or its metadata is unavailable.")
			continue
		}
		uses, err := s.command(ctx, p, "ps", "--all", "--quiet", "--no-trunc", "--filter", "ancestor="+id)
		if err != nil || len(bytes.TrimSpace(uses)) != 0 {
			fmt.Fprintln(w, "Old image retained: it may still be used by another container.")
			continue
		}
		if _, err = s.command(ctx, p, "image", "rm", "--no-prune", id); err != nil {
			fmt.Fprintln(w, "Old image retained: Docker refused removal.")
			continue
		}
		fmt.Fprintln(w, "Removed replaced unused image "+id+".")
	}
}
func (m *Manager) checkImages(id string, automatic bool) error {
	if m.imageContext().Err() != nil {
		return errors.New("image checks are stopping")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.Projects[id]
	if !ok {
		return errors.New("unknown project")
	}
	if m.Images == nil {
		return errors.New("image checks are unavailable")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy || p.ImageUpdates.Checking {
		return errors.New("a project operation or image check is already running")
	}
	interval := time.Minute
	if automatic {
		interval = 6 * time.Hour
	}
	if !p.ImageUpdates.CheckedAt.IsZero() && time.Since(p.ImageUpdates.CheckedAt) < interval {
		return nil
	}
	p.ImageUpdates.Checking = true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		// One project at a time bounds registry requests across automatic/manual checks.
		m.imageCheckMu.Lock()
		defer m.imageCheckMu.Unlock()
		ctx, cancel := context.WithTimeout(m.imageContext(), 5*time.Minute)
		defer cancel()
		report := ImageUpdates{Services: []ImageUpdate{}}
		rev, err := revision(p.Project)
		if err == nil {
			report.Services, err = m.Images.check(ctx, p.Project)
		}
		if err == nil {
			err = sameRevision(p.Project, rev)
		}
		if err != nil {
			report.Services = []ImageUpdate{}
			report.Error = "Image check failed. Verify project files, Docker and registry access."
		} else {
			report.Revision = rev
		}
		report.CheckedAt = time.Now()
		p.mu.Lock()
		p.ImageUpdates = report
		p.mu.Unlock()
	}()
	return nil
}
func (m *Manager) imageContext() context.Context {
	if m.imagesContext != nil {
		return m.imagesContext
	}
	return context.Background()
}
func (m *Manager) startImageChecks(ctx context.Context) {
	m.imagesContext = ctx
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			for _, p := range m.snapshot() {
				if ctx.Err() != nil {
					return
				}
				_ = m.checkImages(p.Project.ID, true)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (s *Server) imageUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if err := s.m.checkImages(r.Form.Get("id"), false); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
