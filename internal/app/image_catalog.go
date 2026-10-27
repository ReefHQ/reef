package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Catalog operations are host-wide, independent of Compose registration.
// Only these selected fields leave Docker; image config/env/history are never exposed.
type CatalogImage struct {
	ID           string   `json:"id"`
	Tags         []string `json:"tags"`
	Digests      []string `json:"digests"`
	Created      string   `json:"created"`
	Size         int64    `json:"size"`
	OS           string   `json:"os"`
	Architecture string   `json:"architecture"`
	Variant      string   `json:"variant"`
	Containers   []string `json:"containers"`
	Fingerprint  string   `json:"fingerprint"`
}
type ImageTarget struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
}
type ImageResult struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}
type CatalogOperation struct {
	ID       string        `json:"id"`
	Action   string        `json:"action"`
	Running  bool          `json:"running"`
	Started  time.Time     `json:"started"`
	Finished time.Time     `json:"finished"`
	Results  []ImageResult `json:"results"`
}

const catalogFormat = `{"id":{{json .Id}},"tags":{{json (index . "RepoTags")}},"digests":{{json (index . "RepoDigests")}},"created":{{json (index . "Created")}},"size":{{.Size}},"os":{{json .Os}},"architecture":{{json .Architecture}},"variant":{{json (index . "Variant")}}}`
const usageFormat = `{"id":{{json .Id}},"image":{{json .Image}},"name":{{json .Name}}}`

var tagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
var repositoryPattern = regexp.MustCompile(`^(?:[a-z0-9]+(?:[.-][a-z0-9]+)*(?::[0-9]+)?/)?[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*)*$`)

func validCatalogReference(ref string, tagOnly bool) bool {
	if len(ref) == 0 || len(ref) > 512 || strings.HasPrefix(ref, "sha256:") {
		return false
	}
	if i := strings.IndexByte(ref, '@'); i >= 0 {
		return !tagOnly && repositoryPattern.MatchString(ref[:i]) && imageIDPattern.MatchString(ref[i+1:])
	}
	name, tag := ref, ""
	if i := strings.LastIndexByte(ref, ':'); i > strings.LastIndexByte(ref, '/') {
		name, tag = ref[:i], ref[i+1:]
		if !tagPattern.MatchString(tag) {
			return false
		}
	}
	return repositoryPattern.MatchString(name) && (!tagOnly || tag != "")
}
func imageFingerprint(row CatalogImage) string {
	tags := append([]string{}, row.Tags...)
	sort.Strings(tags)
	digests := append([]string{}, row.Digests...)
	sort.Strings(digests)
	raw, _ := json.Marshal([]any{row.ID, tags, digests})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *ImageService) catalog(ctx context.Context) ([]CatalogImage, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := s.command(ctx, Project{}, "image", "ls", "--all", "--quiet", "--no-trunc")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(raw))
	seen := map[string]bool{}
	unique := []string{}
	for _, id := range ids {
		if !imageIDPattern.MatchString(id) {
			return nil, errors.New("Invalid Docker image identity")
		}
		if !seen[id] {
			unique = append(unique, id)
			seen[id] = true
		}
	}
	if len(unique) > 2000 {
		return nil, errors.New("Image inventory exceeds 2000 images")
	}
	rows := []CatalogImage{}
	for start := 0; start < len(unique); start += 100 {
		end := start + 100
		if end > len(unique) {
			end = len(unique)
		}
		args := append([]string{"image", "inspect", "--format", catalogFormat}, unique[start:end]...)
		raw, err = s.command(ctx, Project{}, args...)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		count := 0
		for {
			var row CatalogImage
			e := decoder.Decode(&row)
			if e == io.EOF {
				break
			}
			if e != nil || !seen[row.ID] || row.Size < 0 || row.OS == "" || row.Architecture == "" {
				return nil, errors.New("Invalid Docker image metadata")
			}
			count++
			row.Containers = []string{}
			row.Fingerprint = imageFingerprint(row)
			rows = append(rows, row)
		}
		if count != end-start {
			return nil, errors.New("Image inventory changed; refresh and retry")
		}
	}
	raw, err = s.command(ctx, Project{}, "container", "ls", "--all", "--quiet", "--no-trunc")
	if err != nil {
		return nil, err
	}
	containers := strings.Fields(string(raw))
	if len(containers) > 10000 {
		return nil, errors.New("Container inventory exceeds 10000 containers")
	}
	usage := map[string][]string{}
	for start := 0; start < len(containers); start += 100 {
		end := start + 100
		if end > len(containers) {
			end = len(containers)
		}
		for _, id := range containers[start:end] {
			if !containerIDPattern.MatchString(id) {
				return nil, errors.New("Invalid Docker container identity")
			}
		}
		args := append([]string{"container", "inspect", "--format", usageFormat}, containers[start:end]...)
		raw, err = s.command(ctx, Project{}, args...)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		count := 0
		for {
			var row struct{ ID, Image, Name string }
			e := decoder.Decode(&row)
			if e == io.EOF {
				break
			}
			if e != nil || !containerIDPattern.MatchString(row.ID) || !imageIDPattern.MatchString(row.Image) {
				return nil, errors.New("Invalid Docker container metadata")
			}
			count++
			usage[row.Image] = append(usage[row.Image], strings.TrimPrefix(row.Name, "/"))
		}
		if count != end-start {
			return nil, errors.New("Container inventory changed; refresh and retry")
		}
	}
	for i := range rows {
		if names := usage[rows[i].ID]; len(names) > 0 {
			sort.Strings(names)
			rows[i].Containers = names
		}
		sort.Strings(rows[i].Tags)
		sort.Strings(rows[i].Digests)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Created > rows[j].Created || rows[i].Created == rows[j].Created && rows[i].ID < rows[j].ID
	})
	return rows, nil
}
func (s *ImageService) catalogOperation() CatalogOperation {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	op := s.catalogOp
	op.Results = append([]ImageResult{}, op.Results...)
	return op
}
func (s *Server) imageCatalog(w http.ResponseWriter, r *http.Request) {
	if s.m.Images == nil {
		http.Error(w, "Image management unavailable", 503)
		return
	}
	switch r.URL.Path {
	case "/api/images":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		rows, err := s.m.Images.catalog(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		writeJSON(w, map[string]any{"images": rows, "operation": s.m.Images.catalogOperation()})
	case "/api/images/operation":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		writeJSON(w, s.m.Images.catalogOperation())
	case "/api/images/action":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		op, err := s.m.beginCatalog(r.Form.Get("action"), r.Form.Get("reference"), r.Form.Get("tag"), r.Form.Get("targets"))
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		writeJSON(w, op)
	}
}
func (m *Manager) beginCatalog(action, ref, tag, raw string) (CatalogOperation, error) {
	fail := func(message string) (CatalogOperation, error) { return CatalogOperation{}, errors.New(message) }
	if m.Images == nil {
		return fail("Image management unavailable")
	}
	targets := []ImageTarget{}
	switch action {
	case "pull":
		if !validCatalogReference(ref, false) {
			return fail("Enter a valid image reference")
		}
	case "tag", "untag", "remove", "cleanup":
		if json.Unmarshal([]byte(raw), &targets) != nil || len(targets) == 0 || len(targets) > 100 {
			return fail("Select between 1 and 100 images")
		}
		seen := map[string]bool{}
		for _, target := range targets {
			if !imageIDPattern.MatchString(target.ID) || !containerIDPattern.MatchString(target.Fingerprint) || seen[target.ID] {
				return fail("Invalid image selection")
			}
			seen[target.ID] = true
		}
		if (action == "tag" || action == "untag") && len(targets) != 1 {
			return fail("Select one image")
		}
		if (action == "tag" || action == "untag") && !validCatalogReference(tag, true) {
			return fail("Enter an explicit repository:tag")
		}
	default:
		return fail("Unsupported image action")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.Images
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	if m.storageBusy {
		return fail("An image or volume operation is already running")
	}
	for _, p := range m.Projects {
		p.mu.Lock()
		busy := p.busy
		p.mu.Unlock()
		if busy {
			return fail("Wait for Compose operations to finish")
		}
	}
	op := CatalogOperation{ID: randomID(), Action: action, Running: true, Started: time.Now(), Results: []ImageResult{}}
	s.catalogOp = op
	m.storageBusy = true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		results := s.runCatalog(ctx, action, ref, tag, targets)
		m.mu.Lock()
		s.catalogMu.Lock()
		s.catalogOp.Results = results
		s.catalogOp.Running = false
		s.catalogOp.Finished = time.Now()
		m.storageBusy = false
		s.catalogMu.Unlock()
		m.mu.Unlock()
	}()
	return op, nil
}
func (s *ImageService) runCatalog(ctx context.Context, action, ref, tag string, targets []ImageTarget) []ImageResult {
	result := func(id string, err error) ImageResult {
		if err != nil {
			return ImageResult{ID: id, Message: err.Error()}
		}
		return ImageResult{ID: id, OK: true, Message: "Completed"}
	}
	if action == "pull" {
		err := s.Exec.Run(ctx, Project{}, []string{"image", "pull", ref}, io.Discard)
		if err != nil {
			err = errors.New("Pull failed or timed out; check the reference, registry access and host Docker credentials")
		}
		return []ImageResult{result(ref, err)}
	}
	results := []ImageResult{}
	for _, target := range targets {
		err := s.mutateCatalogImage(ctx, action, tag, target)
		results = append(results, result(target.ID, err))
	}
	return results
}
func (s *ImageService) mutateCatalogImage(ctx context.Context, action, tag string, target ImageTarget) error {
	rows, err := s.catalog(ctx)
	if err != nil {
		return err
	}
	var row *CatalogImage
	for i := range rows {
		if rows[i].ID == target.ID {
			row = &rows[i]
		}
		if action == "tag" {
			for _, existing := range rows[i].Tags {
				if existing == tag {
					return errors.New("Destination tag already exists; remove it explicitly before reusing it")
				}
			}
		}
	}
	if row == nil || row.Fingerprint != target.Fingerprint {
		return errors.New("Image or tags changed; refresh and review the selection")
	}
	if action == "tag" {
		_, err = s.command(ctx, Project{}, "image", "tag", row.ID, tag)
		return err
	}
	if len(row.Containers) > 0 {
		return errors.New("Image retained: used by a running or stopped container")
	}
	refs := append([]string{}, row.Tags...)
	if action == "untag" {
		found := false
		for _, ref := range refs {
			if ref == tag {
				found = true
			}
		}
		if !found {
			return errors.New("Tag no longer belongs to this image")
		}
		refs = []string{tag}
	}
	if len(refs) == 0 {
		refs = []string{row.ID}
	}
	for _, ref := range refs {
		// Resolve again immediately before each deletion. Never follow a moved tag.
		metadata, err := s.command(ctx, Project{}, "image", "inspect", "--format", "{{.Id}}", ref)
		if err != nil || strings.TrimSpace(string(metadata)) != row.ID {
			return errors.New("Image reference changed; refresh before retrying")
		}
		uses, err := s.command(ctx, Project{}, "container", "ls", "--all", "--quiet", "--filter", "ancestor="+row.ID)
		if err != nil || len(bytes.TrimSpace(uses)) > 0 {
			return errors.New("Image retained: container usage changed or is unavailable")
		}
		if _, err = s.command(ctx, Project{}, "image", "rm", "--no-prune", ref); err != nil {
			return errors.New("Docker refused removal; refresh to review any tags already removed")
		}
	}
	return nil
}
