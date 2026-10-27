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

// Docker volumes only. No volume contents, driver options or arbitrary labels
// leave the backend, and mountpoint paths are never opened by Reef.
type CatalogVolume struct {
	Name         string   `json:"name"`
	Driver       string   `json:"driver"`
	Scope        string   `json:"scope"`
	Created      string   `json:"created"`
	Mountpoint   string   `json:"mountpoint"`
	Project      string   `json:"project"`
	Containers   []string `json:"containers"`
	Fingerprint  string   `json:"fingerprint"`
	Removable    bool     `json:"removable"`
	RetainReason string   `json:"retain_reason,omitempty"`
}
type volumeMetadata struct {
	Name, Driver, Scope, Created, Mountpoint string
	Labels                                   map[string]string
	Options                                  map[string]string
}
type VolumeTarget struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
}

var volumeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)

const volumeFormat = `{"Name":{{json .Name}},"Driver":{{json .Driver}},"Scope":{{json (index . "Scope")}},"Created":{{json (index . "CreatedAt")}},"Mountpoint":{{json (index . "Mountpoint")}},"Labels":{{json (index . "Labels")}},"Options":{{json (index . "Options")}}}`
const volumeUsageFormat = `{"id":{{json .Id}},"name":{{json .Name}},"mounts":[{{range $i,$m := .Mounts}}{{if $i}},{{end}}{"type":{{json $m.Type}},"name":{{json (index $m "Name")}}}{{end}}]}`

func volumeFingerprint(metadata volumeMetadata) string {
	raw, _ := json.Marshal(metadata)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *ImageService) volumeCommand(ctx context.Context, args ...string) ([]byte, error) {
	raw, err := s.command(ctx, Project{}, args...)
	if err != nil {
		return nil, errors.New("Docker volume command failed; refresh and check host Docker access")
	}
	return raw, nil
}
func (s *ImageService) volumes(ctx context.Context) ([]CatalogVolume, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := s.volumeCommand(ctx, "volume", "ls", "--quiet")
	if err != nil {
		return nil, err
	}
	names := strings.Fields(string(raw))
	if len(names) > 2000 {
		return nil, errors.New("Volume inventory exceeds 2000 volumes")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !volumeNamePattern.MatchString(name) || seen[name] {
			return nil, errors.New("Invalid Docker volume identity")
		}
		seen[name] = true
	}
	rows := []CatalogVolume{}
	for start := 0; start < len(names); start += 100 {
		end := start + 100
		if end > len(names) {
			end = len(names)
		}
		args := append([]string{"volume", "inspect", "--format", volumeFormat}, names[start:end]...)
		raw, err = s.volumeCommand(ctx, args...)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		count := 0
		batch := map[string]bool{}
		for _, name := range names[start:end] {
			batch[name] = true
		}
		for {
			var metadata volumeMetadata
			e := decoder.Decode(&metadata)
			if e == io.EOF {
				break
			}
			if e != nil || !batch[metadata.Name] || metadata.Driver == "" {
				return nil, errors.New("Invalid Docker volume metadata")
			}
			delete(batch, metadata.Name)
			count++
			row := CatalogVolume{Name: metadata.Name, Driver: metadata.Driver, Scope: metadata.Scope, Created: metadata.Created, Mountpoint: metadata.Mountpoint, Project: metadata.Labels["com.docker.compose.project"], Containers: []string{}, Fingerprint: volumeFingerprint(metadata), Removable: metadata.Created != ""}
			if !row.Removable {
				row.RetainReason = "Creation identity unavailable; removal disabled"
			}
			rows = append(rows, row)
		}
		if count != end-start {
			return nil, errors.New("Volume inventory changed; refresh and retry")
		}
	}
	raw, err = s.volumeCommand(ctx, "container", "ls", "--all", "--quiet", "--no-trunc")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(raw))
	if len(ids) > 10000 {
		return nil, errors.New("Container inventory exceeds 10000 containers")
	}
	users := map[string][]string{}
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		batch := map[string]bool{}
		for _, id := range ids[start:end] {
			if !containerIDPattern.MatchString(id) || batch[id] {
				return nil, errors.New("Invalid Docker container identity")
			}
			batch[id] = true
		}
		args := append([]string{"container", "inspect", "--format", volumeUsageFormat}, ids[start:end]...)
		raw, err = s.volumeCommand(ctx, args...)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		count := 0
		for {
			var row struct {
				ID, Name string
				Mounts   []struct{ Type, Name string }
			}
			e := decoder.Decode(&row)
			if e == io.EOF {
				break
			}
			if e != nil || !batch[row.ID] {
				return nil, errors.New("Invalid Docker volume usage")
			}
			delete(batch, row.ID)
			count++
			mounted := map[string]bool{}
			for _, mount := range row.Mounts {
				if mount.Type == "volume" && !mounted[mount.Name] {
					if !volumeNamePattern.MatchString(mount.Name) {
						return nil, errors.New("Invalid mounted volume name")
					}
					users[mount.Name] = append(users[mount.Name], strings.TrimPrefix(row.Name, "/"))
					mounted[mount.Name] = true
				}
			}
		}
		if count != end-start {
			return nil, errors.New("Container inventory changed; refresh and retry")
		}
	}
	for i := range rows {
		if names := users[rows[i].Name]; len(names) > 0 {
			sort.Strings(names)
			rows[i].Containers = names
			rows[i].Removable = false
			rows[i].RetainReason = "Used by a running or stopped container"
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}
func (s *ImageService) volumeOperation() CatalogOperation {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	op := s.volumeOp
	op.Results = append([]ImageResult{}, op.Results...)
	return op
}
func (s *Server) volumeCatalog(w http.ResponseWriter, r *http.Request) {
	if s.m.Images == nil {
		http.Error(w, "Volume management unavailable", 503)
		return
	}
	switch r.URL.Path {
	case "/api/volumes":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		rows, err := s.m.Images.volumes(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		writeJSON(w, map[string]any{"volumes": rows, "operation": s.m.Images.volumeOperation()})
	case "/api/volumes/operation":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		writeJSON(w, s.m.Images.volumeOperation())
	case "/api/volumes/action":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if r.Form.Get("confirmed") != "yes" {
			http.Error(w, "Explicit confirmation required", 400)
			return
		}
		op, err := s.m.beginVolumes(r.Form.Get("action"), r.Form.Get("name"), r.Form.Get("targets"))
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		writeJSON(w, op)
	}
}
func (m *Manager) beginVolumes(action, name, raw string) (CatalogOperation, error) {
	fail := func(message string) (CatalogOperation, error) { return CatalogOperation{}, errors.New(message) }
	if m.Images == nil {
		return fail("Volume management unavailable")
	}
	targets := []VolumeTarget{}
	switch action {
	case "create":
		if !volumeNamePattern.MatchString(name) || len(name) < 2 {
			return fail("Use 2–255 letters, digits, dots, underscores or hyphens; start with a letter or digit")
		}
	case "remove", "cleanup":
		if json.Unmarshal([]byte(raw), &targets) != nil || len(targets) == 0 || len(targets) > 100 {
			return fail("Select between 1 and 100 volumes")
		}
		seen := map[string]bool{}
		for _, target := range targets {
			if !volumeNamePattern.MatchString(target.Name) || !containerIDPattern.MatchString(target.Fingerprint) || seen[target.Name] {
				return fail("Invalid volume selection")
			}
			seen[target.Name] = true
		}
	default:
		return fail("Unsupported volume action")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
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
	s := m.Images
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	op := CatalogOperation{ID: randomID(), Action: action, Running: true, Started: time.Now(), Results: []ImageResult{}}
	s.volumeOp = op
	m.storageBusy = true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		results := s.runVolumes(ctx, action, name, targets)
		m.mu.Lock()
		s.catalogMu.Lock()
		s.volumeOp.Results = results
		s.volumeOp.Running = false
		s.volumeOp.Finished = time.Now()
		m.storageBusy = false
		s.catalogMu.Unlock()
		m.mu.Unlock()
	}()
	return op, nil
}
func (s *ImageService) runVolumes(ctx context.Context, action, name string, targets []VolumeTarget) []ImageResult {
	result := func(name string, err error) ImageResult {
		if err != nil {
			return ImageResult{ID: name, Message: err.Error()}
		}
		return ImageResult{ID: name, OK: true, Message: "Completed"}
	}
	if action == "create" {
		rows, err := s.volumes(ctx)
		if err != nil {
			return []ImageResult{result(name, err)}
		}
		for _, row := range rows {
			if row.Name == name {
				return []ImageResult{result(name, errors.New("A volume with this name already exists"))}
			}
		}
		// A unique label distinguishes a newly created volume from Docker create's
		// idempotent reuse if another client wins a concurrent create of the same name.
		identity := randomID()
		_, err = s.volumeCommand(ctx, "volume", "create", "--driver", "local", "--label", "io.reefhq.created="+identity, name)
		if err == nil {
			raw, e := s.volumeCommand(ctx, "volume", "inspect", "--format", volumeFormat, name)
			var metadata volumeMetadata
			if e != nil || json.Unmarshal(raw, &metadata) != nil || metadata.Labels["io.reefhq.created"] != identity {
				err = errors.New("Volume creation outcome changed or could not be verified; refresh before retrying")
			}
		}
		return []ImageResult{result(name, err)}
	}
	results := []ImageResult{}
	for _, target := range targets {
		results = append(results, result(target.Name, s.removeVolume(ctx, target)))
	}
	return results
}
func (s *ImageService) removeVolume(ctx context.Context, target VolumeTarget) error {
	rows, err := s.volumes(ctx)
	if err != nil {
		return err
	}
	var row *CatalogVolume
	for i := range rows {
		if rows[i].Name == target.Name {
			row = &rows[i]
			break
		}
	}
	if row == nil || row.Fingerprint != target.Fingerprint {
		return errors.New("Volume identity or configuration changed; refresh and review")
	}
	if !row.Removable {
		return errors.New("Volume retained: " + row.RetainReason)
	}
	// Inspect once more immediately before removal. Docker's own non-force remove
	// also refuses references from containers created after our usage snapshot.
	raw, err := s.volumeCommand(ctx, "volume", "inspect", "--format", volumeFormat, target.Name)
	var metadata volumeMetadata
	if err != nil || json.Unmarshal(raw, &metadata) != nil || volumeFingerprint(metadata) != target.Fingerprint {
		return errors.New("Volume identity changed; refresh and review")
	}
	_, err = s.volumeCommand(ctx, "volume", "rm", target.Name)
	if err != nil {
		return errors.New("Docker refused volume removal; refresh usage before retrying")
	}
	return nil
}
