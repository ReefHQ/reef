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
	"sort"
	"strings"
	"time"
)

type CatalogNetwork struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Driver       string   `json:"driver"`
	Scope        string   `json:"scope"`
	Created      string   `json:"created"`
	Project      string   `json:"project"`
	Internal     bool     `json:"internal"`
	IPv6         bool     `json:"ipv6"`
	Attachable   bool     `json:"attachable"`
	Subnets      []string `json:"subnets"`
	Gateways     []string `json:"gateways"`
	Containers   []string `json:"containers"`
	Fingerprint  string   `json:"fingerprint"`
	Removable    bool     `json:"removable"`
	Mutable      bool     `json:"mutable"`
	RetainReason string   `json:"retain_reason,omitempty"`
}
type networkMetadata struct {
	ID, Name, Driver, Scope, Created    string
	Internal, IPv6, Attachable, Ingress bool
	Labels, Options                     map[string]string
	IPAM                                json.RawMessage
	Endpoints                           map[string]struct{ Name string }
}
type NetworkContainer struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	State     string            `json:"state"`
	Protected bool              `json:"protected"`
	Networks  map[string]string `json:"networks"`
	Mode      string            `json:"mode"`
	Project   string            `json:"project"`
}
type NetworkTarget struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
}

var networkNamePattern = volumeNamePattern

const networkFormat = `{"ID":{{json .Id}},"Name":{{json .Name}},"Driver":{{json .Driver}},"Scope":{{json .Scope}},"Created":{{json (index . "Created")}},"Internal":{{json .Internal}},"IPv6":{{json .EnableIPv6}},"Attachable":{{json .Attachable}},"Ingress":{{json .Ingress}},"Labels":{{json .Labels}},"Options":{{json .Options}},"IPAM":{{json .IPAM}},"Endpoints":{{json .Containers}}}`
const networkUsageFormat = `{"id":{{json .Id}},"name":{{json .Name}},"state":{{json .State.Status}},"mode":{{json .HostConfig.NetworkMode}},"project":{{json (index .Config.Labels "com.docker.compose.project")}},"protected":{{if or (index .Config.Labels "io.reef.local") (eq (index .Config.Labels "io.reef.instance") "true")}}true{{else}}false{{end}},"networks":{ {{$first := true}}{{range $i,$n := .NetworkSettings.Networks}}{{if not $first}},{{end}}{{$first = false}}{{json $i}}:{{json $n.NetworkID}}{{end}} }}`

func networkFingerprint(row networkMetadata) string {
	row.Endpoints = nil // Usage is rechecked separately, not part of immutable configuration.
	raw, _ := json.Marshal(row)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *ImageService) networkCommand(ctx context.Context, args ...string) ([]byte, error) {
	raw, err := s.command(ctx, Project{}, args...)
	if err != nil {
		return nil, errors.New("Docker network command failed; refresh and check host Docker access")
	}
	return raw, nil
}
func (s *ImageService) networkInspect(ctx context.Context, id string) (networkMetadata, error) {
	var row networkMetadata
	raw, err := s.networkCommand(ctx, "network", "inspect", "--format", networkFormat, id)
	if err != nil {
		return row, err
	}
	if json.Unmarshal(raw, &row) != nil || row.ID != id || !networkNamePattern.MatchString(row.Name) || row.Driver == "" {
		return row, errors.New("Invalid Docker network metadata")
	}
	return row, nil
}
func (s *ImageService) networks(ctx context.Context) ([]CatalogNetwork, []NetworkContainer, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fail := func(message string) ([]CatalogNetwork, []NetworkContainer, error) {
		return nil, nil, errors.New(message)
	}
	raw, err := s.networkCommand(ctx, "network", "ls", "--quiet", "--no-trunc")
	if err != nil {
		return nil, nil, err
	}
	ids := strings.Fields(string(raw))
	if len(ids) > 2000 {
		return fail("Network inventory exceeds 2000 networks")
	}
	metadata := []networkMetadata{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !containerIDPattern.MatchString(id) || seen[id] {
			return fail("Invalid Docker network identity")
		}
		seen[id] = true
	}
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		batch := map[string]bool{}
		for _, id := range ids[start:end] {
			batch[id] = true
		}
		raw, err = s.networkCommand(ctx, append([]string{"network", "inspect", "--format", networkFormat}, ids[start:end]...)...)
		if err != nil {
			return nil, nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		for {
			var row networkMetadata
			e := dec.Decode(&row)
			if e == io.EOF {
				break
			}
			if e != nil || !batch[row.ID] || !networkNamePattern.MatchString(row.Name) || row.Driver == "" {
				return fail("Invalid Docker network metadata")
			}
			delete(batch, row.ID)
			metadata = append(metadata, row)
		}
		if len(batch) > 0 {
			return fail("Network inventory changed; refresh and retry")
		}
	}
	raw, err = s.networkCommand(ctx, "container", "ls", "--all", "--quiet", "--no-trunc")
	if err != nil {
		return nil, nil, err
	}
	ids = strings.Fields(string(raw))
	if len(ids) > 10000 {
		return fail("Container inventory exceeds 10000 containers")
	}
	containers := []NetworkContainer{}
	seen = map[string]bool{}
	for _, id := range ids {
		if !containerIDPattern.MatchString(id) || seen[id] {
			return fail("Invalid Docker container identity")
		}
		seen[id] = true
	}
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		batch := map[string]bool{}
		for _, id := range ids[start:end] {
			batch[id] = true
		}
		raw, err = s.networkCommand(ctx, append([]string{"container", "inspect", "--format", networkUsageFormat}, ids[start:end]...)...)
		if err != nil {
			return nil, nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		for {
			var row NetworkContainer
			e := dec.Decode(&row)
			if e == io.EOF {
				break
			}
			if e != nil || !batch[row.ID] {
				return fail("Invalid Docker network usage")
			}
			delete(batch, row.ID)
			row.Name = strings.TrimPrefix(row.Name, "/")
			if row.Networks == nil {
				row.Networks = map[string]string{}
			}
			containers = append(containers, row)
		}
		if len(batch) > 0 {
			return fail("Container inventory changed; refresh and retry")
		}
	}
	protectedProjects := map[string]bool{}
	for _, c := range containers {
		if c.Protected && c.Project != "" {
			protectedProjects[c.Project] = true
		}
	}
	for i := range containers {
		if protectedProjects[containers[i].Project] {
			containers[i].Protected = true
		}
	}
	// Shared network namespaces must not provide a way around Reef protection.
	for range containers {
		changed := false
		for _, c := range containers {
			if !c.Protected || !strings.HasPrefix(c.Mode, "container:") {
				continue
			}
			target := strings.TrimPrefix(c.Mode, "container:")
			for j, other := range containers {
				if (other.ID == target || other.Name == target) && !other.Protected {
					containers[j].Protected = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	rows := []CatalogNetwork{}
	for _, m := range metadata {
		row := CatalogNetwork{ID: m.ID, Name: m.Name, Driver: m.Driver, Scope: m.Scope, Created: m.Created, Project: m.Labels["com.docker.compose.project"], Internal: m.Internal, IPv6: m.IPv6, Attachable: m.Attachable, Subnets: []string{}, Gateways: []string{}, Containers: []string{}, Fingerprint: networkFingerprint(m)}
		var ipam struct {
			Config []struct{ Subnet, Gateway string }
		}
		if json.Unmarshal(m.IPAM, &ipam) != nil {
			return fail("Invalid Docker network addressing")
		}
		for _, cfg := range ipam.Config {
			if cfg.Subnet != "" {
				row.Subnets = append(row.Subnets, cfg.Subnet)
			}
			if cfg.Gateway != "" {
				row.Gateways = append(row.Gateways, cfg.Gateway)
			}
		}
		protected := false
		users := map[string]bool{}
		for _, c := range containers {
			for name, id := range c.Networks {
				if id == m.ID || name == m.Name {
					users[c.Name] = true
					if c.Protected {
						protected = true
					}
				}
			}
		}
		// Count every endpoint, including one added between the two snapshots.
		for id, ep := range m.Endpoints {
			name := ep.Name + " (endpoint)"
			for _, c := range containers {
				if c.ID == id {
					name = c.Name
					if c.Protected {
						protected = true
					}
					break
				}
			}
			users[name] = true
		}
		for name := range users {
			row.Containers = append(row.Containers, name)
		}
		sort.Strings(row.Containers)
		row.Mutable = m.Driver == "bridge" && m.Scope == "local" && m.Name != "bridge" && m.Name != "host" && m.Name != "none" && !m.Ingress
		if !row.Mutable {
			row.RetainReason = "Built-in network or unsupported driver/scope; inspection only"
		} else if protected {
			row.Mutable = false
			row.RetainReason = "Reef administration network; inspection only"
		} else if len(row.Containers) > 0 {
			row.RetainReason = "Referenced by running or stopped containers"
		} else {
			row.Removable = true
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })
	return rows, containers, nil
}

func (s *ImageService) networkOperation() CatalogOperation {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	op := s.networkOp
	op.Results = append([]ImageResult{}, op.Results...)
	return op
}
func (s *Server) networkCatalog(w http.ResponseWriter, r *http.Request) {
	if s.m.Images == nil {
		http.Error(w, "Network management unavailable", 503)
		return
	}
	switch r.URL.Path {
	case "/api/networks":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		rows, containers, err := s.m.Images.networks(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		writeJSON(w, map[string]any{"networks": rows, "containers": containers, "operation": s.m.Images.networkOperation()})
	case "/api/networks/operation":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		writeJSON(w, s.m.Images.networkOperation())
	case "/api/networks/action":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if r.Form.Get("confirmed") != "yes" {
			http.Error(w, "Explicit confirmation required", 400)
			return
		}
		op, err := s.m.beginNetworks(r.Form.Get("action"), r.Form.Get("name"), r.Form.Get("targets"), r.Form.Get("container"))
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		writeJSON(w, op)
	}
}
func (m *Manager) beginNetworks(action, name, raw, container string) (CatalogOperation, error) {
	fail := func(message string) (CatalogOperation, error) { return CatalogOperation{}, errors.New(message) }
	if m.Images == nil {
		return fail("Network management unavailable")
	}
	targets := []NetworkTarget{}
	switch action {
	case "create":
		if !networkNamePattern.MatchString(name) || len(name) < 2 {
			return fail("Use 2–255 letters, digits, dots, underscores or hyphens; start with a letter or digit")
		}
	case "connect", "disconnect", "remove", "cleanup":
		if json.Unmarshal([]byte(raw), &targets) != nil || len(targets) == 0 || len(targets) > 2000 {
			return fail("Select between 1 and 2000 networks")
		}
		seen := map[string]bool{}
		for _, target := range targets {
			if !containerIDPattern.MatchString(target.ID) || !containerIDPattern.MatchString(target.Fingerprint) || seen[target.ID] {
				return fail("Invalid network selection")
			}
			seen[target.ID] = true
		}
	default:
		return fail("Unsupported network action")
	}
	if action == "connect" || action == "disconnect" {
		if len(targets) != 1 || !containerIDPattern.MatchString(container) {
			return fail("Select one network and a valid container")
		}
	}
	eventTarget := name
	if eventTarget == "" && len(targets) == 1 {
		eventTarget = targets[0].ID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storageBusy {
		return fail("An image or network operation is already running")
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
	op.Total = len(targets)
	if action == "create" {
		op.Total = 1
	}
	s.networkOp = op
	m.catalogEvent("network", action, "started", op.ID, eventTarget, op.Total, nil)
	m.storageBusy = true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		results := s.runNetworks(ctx, action, name, targets, container)
		m.mu.Lock()
		s.catalogMu.Lock()
		s.networkOp.Results = results
		m.catalogEvent("network", action, "completed", op.ID, eventTarget, op.Total, s.networkOp.Results)
		s.networkOp.Running = false
		s.networkOp.Finished = time.Now()
		m.storageBusy = false
		s.catalogMu.Unlock()
		m.mu.Unlock()
	}()
	return op, nil
}

func (s *ImageService) runNetworks(ctx context.Context, action, name string, targets []NetworkTarget, container string) []ImageResult {
	result := func(id string, err error) ImageResult {
		if err != nil {
			return ImageResult{ID: id, Message: err.Error()}
		}
		return ImageResult{ID: id, OK: true, Message: "Completed"}
	}
	if action == "create" {
		rows, _, err := s.networks(ctx)
		if err != nil {
			return []ImageResult{result(name, err)}
		}
		for _, row := range rows {
			if row.Name == name {
				return []ImageResult{result(name, errors.New("A network with this name already exists"))}
			}
		}
		identity := randomID()
		raw, err := s.networkCommand(ctx, "network", "create", "--driver", "bridge", "--label", "io.reefhq.created="+identity, name)
		if err == nil {
			id := strings.TrimSpace(string(raw))
			var row networkMetadata
			if !containerIDPattern.MatchString(id) {
				err = errors.New("Network creation outcome unknown; refresh before retrying")
			} else {
				row, err = s.networkInspect(ctx, id)
				if err == nil && (row.Name != name || row.Labels["io.reefhq.created"] != identity) {
					err = errors.New("Network creation outcome changed; refresh before retrying")
				}
			}
		}
		return []ImageResult{result(name, err)}
	}
	results := []ImageResult{}
	for _, target := range targets {
		if ctx.Err() != nil {
			results = append(results, ImageResult{ID: target.ID, Unprocessed: true, Message: "Operation deadline reached; refresh before retrying"})
			continue
		}
		results = append(results, result(target.ID, s.mutateNetwork(ctx, action, target, container)))
	}
	return results
}
func (s *ImageService) mutateNetwork(ctx context.Context, action string, target NetworkTarget, container string) error {
	rows, containers, err := s.networks(ctx)
	if err != nil {
		return err
	}
	var row *CatalogNetwork
	for i := range rows {
		if rows[i].ID == target.ID {
			row = &rows[i]
			break
		}
	}
	if row == nil || row.Fingerprint != target.Fingerprint {
		return errors.New("Network identity or configuration changed; refresh and review")
	}
	if !row.Mutable {
		return errors.New("Network retained: " + row.RetainReason)
	}
	if action == "remove" || action == "cleanup" {
		if !row.Removable {
			return errors.New("Network retained: " + row.RetainReason)
		}
	} else {
		var chosen *NetworkContainer
		for i := range containers {
			if containers[i].ID == container {
				chosen = &containers[i]
				break
			}
		}
		if chosen == nil || chosen.Protected || chosen.State != "running" || strings.HasPrefix(chosen.Mode, "container:") || chosen.Mode == "host" || chosen.Mode == "none" {
			return errors.New("Select a running container with its own network namespace; Reef containers are protected")
		}
		connected := false
		for name, id := range chosen.Networks {
			if id == row.ID || name == row.Name {
				connected = true
			}
		}
		if (action == "connect" && connected) || (action == "disconnect" && !connected) {
			return errors.New("Container connection changed; refresh and review")
		}
	}
	// Use immutable IDs, never names or --force. Docker itself rejects active endpoints.
	fresh, err := s.networkInspect(ctx, target.ID)
	if err != nil || networkFingerprint(fresh) != target.Fingerprint {
		return errors.New("Network identity changed; refresh and review")
	}
	switch action {
	case "remove", "cleanup":
		_, err = s.networkCommand(ctx, "network", "rm", target.ID)
	case "connect", "disconnect":
		_, err = s.networkCommand(ctx, "network", action, target.ID, container)
	default:
		return errors.New("Unsupported network action")
	}
	return err
}
