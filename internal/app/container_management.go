package app

// Standalone management deliberately keeps inspect/create payloads host-local.
// They can contain environment secrets; only the curated review and journal DTOs
// below may leave this file. Browser requests never supply a Docker configuration.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type containerAPI struct{ http *http.Client }

func newContainerAPI(socket string) *containerAPI {
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	return &containerAPI{http: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second}}}
}
func (a *containerAPI) call(ctx context.Context, method, path string, body any, out any) error {
	var b []byte
	var err error
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return errors.New("Cannot prepare Docker request")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, bytes.NewReader(b))
	if err != nil {
		return errors.New("Cannot prepare Docker request")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := a.http.Do(req)
	if err != nil {
		return errors.New("Docker request interrupted; inspect host state before retrying")
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return fmt.Errorf("Docker request rejected (HTTP %d); inspect host state before retrying", res.StatusCode)
	}
	b, err = io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil || len(b) > 4<<20 {
		return errors.New("Docker response unavailable or oversized; inspect host state before retrying")
	}
	if out != nil && json.Unmarshal(b, out) != nil {
		return errors.New("Invalid Docker response; inspect host state before retrying")
	}
	return nil
}

type inspectedContainer struct {
	ID                 string `json:"Id"`
	Name, Image        string
	Config, HostConfig map[string]json.RawMessage
	State              struct {
		Status                      string
		Running, Paused, Restarting bool
		StartedAt                   string
		Health                      *struct{ Status string }
	}
	Mounts []struct {
		Type, Name, Source, Destination, Mode, Propagation string
		RW                                                 bool
	}
	NetworkSettings struct {
		Networks map[string]map[string]json.RawMessage
		Ports    map[string]json.RawMessage
	}
}

func value[T any](m map[string]json.RawMessage, key string) T {
	var v T
	json.Unmarshal(m[key], &v)
	return v
}
func put(m map[string]json.RawMessage, key string, v any) { m[key], _ = json.Marshal(v) }
func inspectFingerprint(c inspectedContainer) string {
	b, _ := json.Marshal([]any{c.ID, c.Name, c.Image, c.Config, c.HostConfig, c.Mounts, c.NetworkSettings.Networks, c.NetworkSettings.Ports, c.State.Status, c.State.StartedAt})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type containerReview struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Image         string   `json:"image"`
	ImageID       string   `json:"image_id"`
	State         string   `json:"state"`
	Fingerprint   string   `json:"fingerprint"`
	Allowed       bool     `json:"allowed"`
	Reason        string   `json:"reason"`
	UpdateAllowed bool     `json:"update_allowed"`
	UpdateReason  string   `json:"update_reason"`
	MountCount    int      `json:"mount_count"`
	Networks      []string `json:"networks"`
	RestartPolicy string   `json:"restart_policy"`
}
type containerOperation struct {
	PersistenceError bool      `json:"persistence_error,omitempty"`
	ID               string    `json:"id"`
	Action           string    `json:"action"`
	ContainerID      string    `json:"container_id"`
	Name             string    `json:"name"`
	Phase            string    `json:"phase"`
	Running          bool      `json:"running"`
	Unknown          bool      `json:"unknown"`
	OK               bool      `json:"ok"`
	Message          string    `json:"message"`
	NewID            string    `json:"new_id,omitempty"`
	TemporaryName    string    `json:"temporary_name,omitempty"`
	PreviousName     string    `json:"previous_name,omitempty"`
	Started          time.Time `json:"started"`
	Finished         time.Time `json:"finished"`
}
type containerManagement struct {
	mu   sync.Mutex
	api  *containerAPI
	path string
	op   containerOperation
	err  error
}

func openContainerManagement(state, socket string) *containerManagement {
	s := &containerManagement{api: newContainerAPI(socket)}
	if state == "" {
		return s
	}
	s.path = filepath.Join(state, "container-operation.json")
	if e := rejectSymlinks(s.path); e != nil && !os.IsNotExist(e) {
		s.err = errors.New("Container operation state is unsafe")
		return s
	}
	i, e := os.Lstat(s.path)
	if os.IsNotExist(e) {
		return s
	}
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 || i.Size() > 65536 {
		s.err = errors.New("Container operation state is unsafe")
		return s
	}
	b, e := os.ReadFile(s.path)
	if e != nil || json.Unmarshal(b, &s.op) != nil || (len(s.op.ID) != 48 || strings.Trim(s.op.ID, "0123456789abcdef") != "") || !containerIDPattern.MatchString(s.op.ContainerID) {
		s.err = errors.New("Container operation state is invalid")
		return s
	}
	if s.op.Running {
		s.op.Running = false
		s.op.Unknown = true
		s.op.Phase = "review_required"
		s.op.Message = "Reef restarted during an operation. Inspect old/new containers and network/restart settings before acknowledging; no action was replayed."
		s.err = s.persist()
	}
	return s
}
func (s *containerManagement) persist() error {
	if s.path == "" {
		return nil
	}
	if e := rejectSymlinks(s.path); e != nil && !os.IsNotExist(e) {
		return errors.New("Cannot persist container operation")
	}
	if i, e := os.Lstat(s.path); e == nil && (!i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0) {
		return errors.New("Cannot persist container operation")
	}
	b, e := json.Marshal(s.op)
	if e != nil {
		return e
	}
	if e = atomicWrite(s.path, b, 0600); e != nil {
		return errors.New("Cannot persist container operation; inspect host state")
	}
	return nil
}
func (s *containerManagement) snapshot() (containerOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.op, s.err
}
func (s *containerManagement) checkpoint(phase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.op.Phase = phase
	s.err = s.persist()
	return s.err
}
func (s *containerManagement) inspect(ctx context.Context, id string) (inspectedContainer, error) {
	var c inspectedContainer
	if !containerIDPattern.MatchString(id) {
		return c, errors.New("Use a full container ID")
	}
	e := s.api.call(ctx, "GET", "/containers/"+id+"/json", nil, &c)
	if e == nil && (c.ID != id || c.Config == nil || c.HostConfig == nil) {
		e = errors.New("Container identity or configuration unavailable")
	}
	return c, e
}
func (s *containerManagement) review(ctx context.Context, id string) (containerReview, inspectedContainer, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, e := s.inspect(ctx, id)
	if e != nil {
		return containerReview{}, c, e
	}
	r := containerReview{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), Image: value[string](c.Config, "Image"), ImageID: c.Image, State: c.State.Status, Fingerprint: inspectFingerprint(c), Allowed: true, UpdateAllowed: true, MountCount: len(c.Mounts), Networks: []string{}, RestartPolicy: value[struct{ Name string }](c.HostConfig, "RestartPolicy").Name}
	for name := range c.NetworkSettings.Networks {
		r.Networks = append(r.Networks, name)
	}
	sort.Strings(r.Networks)
	labels := value[map[string]string](c.Config, "Labels")
	for k, v := range labels {
		if strings.HasPrefix(k, "com.docker.compose.") || strings.HasPrefix(k, "com.docker.swarm.") || k == "io.reef.local" || k == "io.reef.instance" && v == "true" {
			r.Allowed = false
			r.Reason = "Compose, Swarm and Reef administration containers must be managed by their owner/project"
		}
	}
	hostname, _ := os.Hostname()
	if len(hostname) >= 12 && strings.HasPrefix(c.ID, hostname) {
		r.Allowed = false
		r.Reason = "Reef cannot manage its own container"
	}
	for _, m := range c.Mounts {
		if m.Destination == "/var/run/docker.sock" || m.Destination == "/run/docker.sock" || strings.HasSuffix(m.Source, "/docker.sock") {
			r.Allowed = false
			r.Reason = "Docker administration containers are protected"
		}
	}
	if c.State.Paused || c.State.Restarting || (c.State.Status != "running" && c.State.Status != "exited" && c.State.Status != "created") {
		r.Allowed = false
		r.Reason = "Wait for a stable running, exited or created container"
	}
	if value[bool](c.HostConfig, "AutoRemove") {
		r.Allowed = false
		r.Reason = "Auto-remove containers cannot be safely stopped or recreated"
	}
	for _, k := range []string{"NetworkMode", "PidMode", "IpcMode"} {
		if strings.HasPrefix(value[string](c.HostConfig, k), "container:") {
			r.Allowed = false
			r.Reason = "Containers sharing another container's namespace require owner-managed operations"
		}
	}
	// Fail closed if another container depends on this one's network/PID/IPC or volumes.
	var all []struct {
		ID string `json:"Id"`
	}
	if e = s.api.call(ctx, "GET", "/containers/json?all=1", nil, &all); e != nil {
		return r, c, e
	}
	if len(all) > 2000 {
		return r, c, errors.New("Dependency scan exceeds 2000 containers")
	}
	for _, other := range all {
		if other.ID == id {
			continue
		}
		v, err := s.inspect(ctx, other.ID)
		if err != nil {
			return r, c, errors.New("Container dependencies changed or could not be checked")
		}
		for _, k := range []string{"NetworkMode", "PidMode", "IpcMode"} {
			ref := strings.TrimPrefix(value[string](v.HostConfig, k), "container:")
			if ref != value[string](v.HostConfig, k) && (ref == r.Name || ref == id || len(ref) >= 12 && strings.HasPrefix(id, ref)) {
				r.Allowed = false
				r.Reason = "Another container shares this container's namespace"
			}
		}
		for _, ref := range value[[]string](v.HostConfig, "VolumesFrom") {
			ref = strings.Split(ref, ":")[0]
			if ref == r.Name || ref == id || len(ref) >= 12 && strings.HasPrefix(id, ref) {
				r.Allowed = false
				r.Reason = "Another container inherits volumes from this container"
			}
		}
	}
	if _, e = containerCreatePlan(c, c.Image, "preflight"); e != nil {
		r.UpdateAllowed = false
		r.UpdateReason = e.Error()
	}
	if r.UpdateAllowed {
		for name, ep := range c.NetworkSettings.Networks {
			var network struct {
				ID            string `json:"Id"`
				Driver, Scope string
				Ingress       bool
			}
			if err := s.api.call(ctx, "GET", "/networks/"+url.PathEscape(name), nil, &network); err != nil {
				return r, c, err
			}
			if network.Scope != "local" || network.Ingress || (network.Driver != "bridge" && network.Driver != "null") {
				r.UpdateAllowed = false
				r.UpdateReason = "Image recreation supports ordinary local bridge/none networks; use the original deployment definition for this network"
			}
			if expected := value[string](ep, "NetworkID"); expected != "" && network.ID != "" && network.ID != expected {
				return r, c, errors.New("Network identity changed; refresh and review")
			}
		}
	}
	if !validCatalogReference(r.Image, false) {
		r.UpdateAllowed = false
		r.UpdateReason = "The image must have a registry reference that can be pulled"
	}
	if !r.Allowed {
		r.UpdateAllowed = false
		r.UpdateReason = r.Reason
	}
	return r, c, nil
}

// Preserve all host/config fields from Docker rather than reconstructing CLI flags.
// Explicitly resolve every volume (including anonymous/image-declared volumes).
func containerCreatePlan(c inspectedContainer, image, marker string) (map[string]json.RawMessage, error) {
	fail := func() (map[string]json.RawMessage, error) {
		return nil, errors.New("This container configuration cannot be reproduced safely; use its original deployment definition")
	}
	h := c.HostConfig
	for _, k := range []string{"VolumesFrom", "Links"} {
		if len(value[[]string](h, k)) > 0 {
			return fail()
		}
	}
	if value[string](h, "ContainerIDFile") != "" || value[bool](h, "AutoRemove") {
		return fail()
	}
	mode := value[string](h, "NetworkMode")
	if mode == "host" || strings.HasPrefix(mode, "container:") {
		return fail()
	}
	for _, k := range []string{"PidMode", "IpcMode"} {
		if strings.HasPrefix(value[string](h, k), "container:") {
			return fail()
		}
	}
	// Device requests and security settings stay in the host config unchanged.
	body := map[string]json.RawMessage{}
	for k, v := range c.Config {
		body[k] = append(json.RawMessage{}, v...)
	}
	put(body, "Image", image)
	labels := value[map[string]string](c.Config, "Labels")
	if labels == nil {
		labels = map[string]string{}
	}
	labels["io.reefhq.replacement"] = marker
	put(body, "Labels", labels)
	host := map[string]json.RawMessage{}
	for k, v := range h {
		host[k] = append(json.RawMessage{}, v...)
	}
	structured := value[[]map[string]json.RawMessage](h, "Mounts")
	targets := map[string]bool{}
	for _, m := range structured {
		typ := value[string](m, "Type")
		dst := value[string](m, "Target")
		if typ != "volume" && typ != "bind" && typ != "tmpfs" {
			return fail()
		}
		targets[dst] = true
		if typ == "volume" {
			found := false
			for _, actual := range c.Mounts {
				if actual.Destination == dst && actual.Type == "volume" && actual.Name != "" {
					put(m, "Source", actual.Name)
					found = true
				}
			}
			if !found {
				return fail()
			}
		}
	}
	binds := []string{}
	for _, m := range c.Mounts {
		if targets[m.Destination] || m.Type == "tmpfs" {
			continue
		}
		src := m.Source
		if m.Type == "volume" {
			src = m.Name
		} else if m.Type != "bind" {
			return fail()
		}
		if src == "" || m.Destination == "" || strings.Contains(src, ":") || strings.Contains(m.Destination, ":") {
			return fail()
		}
		options := m.Mode
		if options == "" {
			options = "rw"
			if !m.RW {
				options = "ro"
			}
		}
		if m.Type == "bind" && m.Propagation != "" && !strings.Contains(options, m.Propagation) {
			options += "," + m.Propagation
		}
		binds = append(binds, src+":"+m.Destination+":"+options)
	}
	put(host, "Binds", binds)
	put(host, "Mounts", structured)
	// Keep allocated host ports for running containers originally started with -P
	// or a random published port, rather than silently changing their access URL.
	if c.State.Running && len(c.NetworkSettings.Ports) > 0 {
		put(host, "PortBindings", c.NetworkSettings.Ports)
	}
	put(body, "HostConfig", host)
	endpoints := map[string]map[string]json.RawMessage{}
	for name, ep := range c.NetworkSettings.Networks {
		if len(value[[]string](ep, "Links")) > 0 {
			return fail()
		}
		dst := map[string]json.RawMessage{}
		for _, k := range []string{"IPAMConfig", "DriverOpts", "GwPriority", "MacAddress"} {
			if v, ok := ep[k]; ok {
				dst[k] = v
			}
		}
		aliases := []string{}
		for _, a := range value[[]string](ep, "Aliases") {
			if a != c.ID && a != c.ID[:12] {
				aliases = append(aliases, a)
			}
		}
		if name != "bridge" && name != "host" && name != "none" {
			put(dst, "Aliases", aliases)
		}
		endpoints[name] = dst
	}
	put(body, "NetworkingConfig", map[string]any{"EndpointsConfig": endpoints})
	return body, nil
}

func (s *Server) containerManagementAPI(w http.ResponseWriter, r *http.Request) {
	c := s.m.containers
	if c == nil {
		http.Error(w, "Container management unavailable", 503)
		return
	}
	op, e := c.snapshot()
	if e != nil {
		if r.Method == "GET" && r.URL.Path == "/api/container-management/operation" {
			op.PersistenceError = true
			op.Unknown = true
			op.Phase = "review_required"
			op.Message = "Container operation storage unavailable; inspect host state and repair private operation storage before retrying"
			writeJSON(w, op)
			return
		}
		http.Error(w, e.Error(), 503)
		return
	}
	switch r.URL.Path {
	case "/api/container-management/operation":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		writeJSON(w, op)
	case "/api/container-management":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		review, _, e := c.review(r.Context(), r.URL.Query().Get("id"))
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		writeJSON(w, map[string]any{"container": review, "operation": op})
	case "/api/container-management/action":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if r.Form.Get("confirmed") != "yes" {
			http.Error(w, "Explicit confirmation required", 400)
			return
		}
		if (r.Form.Get("action") == "update" || r.Form.Get("action") == "remove") && r.Form.Get("data_reviewed") != "yes" {
			http.Error(w, "Review downtime, writable-layer loss and volume retention", 400)
			return
		}
		op, e := s.m.beginContainer(r.Form.Get("action"), r.Form.Get("id"), r.Form.Get("fingerprint"))
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		w.WriteHeader(202)
		writeJSON(w, op)
	case "/api/container-management/acknowledge":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		s.m.mu.Lock()
		defer s.m.mu.Unlock()
		c.mu.Lock()
		defer c.mu.Unlock()
		if r.Form.Get("confirmed") != "yes" || r.Form.Get("operation") != c.op.ID || !c.op.Unknown || c.op.Running {
			http.Error(w, "Review the current interrupted operation first", 409)
			return
		}
		c.op.Unknown = false
		c.op.Phase = "acknowledged"
		c.op.Message = "Host state reviewed; no Docker action was replayed"
		c.err = c.persist()
		if c.err != nil {
			http.Error(w, c.err.Error(), 503)
			return
		}
		s.m.storageBusy = false
		writeJSON(w, c.op)
	}
}

func (m *Manager) beginContainer(action, id, fingerprint string) (containerOperation, error) {
	fail := func(msg string) (containerOperation, error) { return containerOperation{}, errors.New(msg) }
	if action != "start" && action != "stop" && action != "restart" && action != "remove" && action != "update" {
		return fail("Unsupported container action")
	}
	if !containerIDPattern.MatchString(id) || !containerIDPattern.MatchString(fingerprint) {
		return fail("Review a full container identity and current configuration")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.containers
	if c == nil {
		return fail("Container management unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return containerOperation{}, c.err
	}
	if m.storageBusy || m.agentUpdating || c.op.Running || c.op.Unknown {
		return fail("Wait for current operations or review the interrupted container operation")
	}
	for _, p := range m.Projects {
		p.mu.Lock()
		busy := p.busy
		p.mu.Unlock()
		if busy {
			return fail("Wait for Compose operations to finish")
		}
	}
	op := containerOperation{ID: randomID(), Action: action, ContainerID: id, Phase: "reviewing", Running: true, Started: time.Now()}
	if action == "update" {
		op.TemporaryName = "reef-next-" + op.ID[:12]
		op.PreviousName = "reef-previous-" + op.ID[:12]
	}
	c.op = op
	if e := c.persist(); e != nil {
		c.err = e
		return containerOperation{}, e
	}
	m.storageBusy = true
	m.wg.Add(1)
	m.catalogEvent("container", action, "started", op.ID, id, 1, nil)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		err := c.run(ctx, m, op, fingerprint)
		m.mu.Lock()
		defer m.mu.Unlock()
		c.mu.Lock()
		defer c.mu.Unlock()
		c.op.Running = false
		c.op.OK = err == nil
		c.op.Finished = time.Now()
		if err != nil {
			c.op.Message = err.Error()
			if c.op.Phase != "rolled_back" && c.op.Phase != "reviewing" && c.op.Phase != "pulling" {
				c.op.Unknown = true
				c.op.Phase = "review_required"
			}
		} else {
			c.op.Phase = "completed"
			if c.op.Message == "" {
				c.op.Message = "Completed; existing volumes and images retained"
			}
		}
		if e := c.persist(); e != nil {
			c.err = e
		}
		m.storageBusy = c.op.Unknown || c.err != nil
		m.catalogEvent("container", action, "completed", op.ID, id, 1, []ImageResult{{ID: id, OK: err == nil, Message: c.op.Message}})
	}()
	return op, nil
}

func (c *containerManagement) mutate(ctx context.Context, phase, method, path string, body any, out any) error {
	if e := c.checkpoint(phase); e != nil {
		return e
	}
	return c.api.call(ctx, method, path, body, out)
}
func (c *containerManagement) run(ctx context.Context, m *Manager, op containerOperation, fingerprint string) error {
	r, old, e := c.review(ctx, op.ContainerID)
	if e != nil {
		return e
	}
	if !r.Allowed || r.Fingerprint != fingerprint {
		return errors.New("Container identity, state, configuration or eligibility changed; refresh and review")
	}
	c.mu.Lock()
	c.op.Name = r.Name
	c.mu.Unlock()
	if op.Action == "update" {
		if !r.UpdateAllowed {
			return errors.New(r.UpdateReason)
		}
		return c.replace(ctx, m, op, old, fingerprint)
	}
	if op.Action == "start" && old.State.Running || op.Action == "stop" && !old.State.Running {
		return errors.New("Container state changed; refresh and review")
	}
	base := "/containers/" + old.ID
	if op.Action == "remove" {
		if old.State.Running {
			if e = c.mutate(ctx, "stopping", "POST", base+"/stop?t=20", nil, nil); e != nil {
				return e
			}
		}
		return c.mutate(ctx, "removing", "DELETE", base+"?v=false&force=false", nil, nil)
	}
	if op.Action == "restart" && !old.State.Running {
		return errors.New("Restart requires a running container")
	}
	return c.mutate(ctx, op.Action, "POST", base+"/"+op.Action+"?t=20", nil, nil)
}
func (c *containerManagement) replace(ctx context.Context, m *Manager, op containerOperation, old inspectedContainer, fingerprint string) error {
	if e := c.checkpoint("pulling"); e != nil {
		return e
	}
	ref := value[string](old.Config, "Image")
	if e := m.Exec.Run(ctx, Project{}, []string{"image", "pull", ref}, io.Discard); e != nil {
		return errors.New("Image pull failed before downtime; existing container retained")
	}
	var image struct {
		ID                        string `json:"Id"`
		Os, Architecture, Variant string
	}
	var previous struct{ Os, Architecture, Variant string }
	if e := c.api.call(ctx, "GET", "/images/"+url.PathEscape(ref)+"/json", nil, &image); e != nil {
		return e
	}
	if !imageIDPattern.MatchString(image.ID) {
		return errors.New("Pulled image identity unavailable")
	}
	if image.ID == old.Image {
		c.mu.Lock()
		c.op.Message = "Image already current; no container recreated"
		c.mu.Unlock()
		return nil
	}
	if e := c.api.call(ctx, "GET", "/images/"+old.Image+"/json", nil, &previous); e != nil {
		return e
	}
	if previous.Os != "linux" || image.Os != previous.Os || image.Architecture != previous.Architecture || image.Variant != previous.Variant {
		return errors.New("Image platform changed; existing container retained")
	}
	// Only ordinary local bridge/none networks can be faithfully reattached here.
	for name := range old.NetworkSettings.Networks {
		var network struct {
			Driver, Scope string
			Ingress       bool
		}
		if e := c.api.call(ctx, "GET", "/networks/"+url.PathEscape(name), nil, &network); e != nil {
			return e
		}
		if network.Scope != "local" || network.Ingress || (network.Driver != "bridge" && network.Driver != "null") {
			return errors.New("Unsupported network driver or scope; existing container retained")
		}
	}
	body, e := containerCreatePlan(old, image.ID, op.ID)
	if e != nil {
		return e
	}
	// Recheck after a potentially long pull, before any container mutation.
	r, fresh, e := c.review(ctx, old.ID)
	if e != nil {
		return e
	}
	if !r.Allowed || !r.UpdateAllowed || inspectFingerprint(fresh) != fingerprint {
		return errors.New("Container changed during pull; existing container retained")
	}
	var created struct {
		ID string `json:"Id"`
	}
	if e = c.mutate(ctx, "creating_replacement", "POST", "/containers/create?name="+op.TemporaryName, body, &created); e != nil {
		return e
	}
	if !containerIDPattern.MatchString(created.ID) {
		return errors.New("Replacement identity unknown; inspect temporary container before retrying")
	}
	c.mu.Lock()
	c.op.NewID = created.ID
	c.mu.Unlock()
	if e = c.checkpoint("replacement_created"); e != nil {
		return e
	}
	latest, checkErr := c.inspect(ctx, old.ID)
	if checkErr != nil || inspectFingerprint(latest) != fingerprint {
		if e = c.mutate(ctx, "discarding_replacement", "DELETE", "/containers/"+created.ID+"?v=false&force=false", nil, nil); e != nil {
			return errors.New("Container changed during preparation; replacement cleanup needs review")
		}
		if e = c.checkpoint("rolled_back"); e != nil {
			return e
		}
		return errors.New("Container changed during preparation; stopped replacement discarded and original left unchanged")
	}
	// From here every failure attempts a bounded, identity-checked rollback. A
	// persistence failure stops all further Docker writes, including rollback.
	result := func() error {
		if e = c.mutate(ctx, "disabling_previous_restart", "POST", "/containers/"+old.ID+"/update", map[string]any{"RestartPolicy": map[string]any{"Name": "no", "MaximumRetryCount": 0}}, nil); e != nil {
			return e
		}
		if old.State.Running {
			if e = c.mutate(ctx, "stopping_previous", "POST", "/containers/"+old.ID+"/stop?t=20", nil, nil); e != nil {
				return e
			}
		}
		if e = c.mutate(ctx, "retaining_previous", "POST", "/containers/"+old.ID+"/rename?name="+op.PreviousName, nil, nil); e != nil {
			return e
		}
		for name := range old.NetworkSettings.Networks {
			if e = c.mutate(ctx, "releasing_previous_network", "POST", "/networks/"+url.PathEscape(name)+"/disconnect", map[string]any{"Container": old.ID, "Force": false}, nil); e != nil {
				return e
			}
		}
		if e = c.mutate(ctx, "naming_replacement", "POST", "/containers/"+created.ID+"/rename?name="+url.QueryEscape(strings.TrimPrefix(old.Name, "/")), nil, nil); e != nil {
			return e
		}
		if old.State.Running {
			if e = c.mutate(ctx, "starting_replacement", "POST", "/containers/"+created.ID+"/start", nil, nil); e != nil {
				return e
			}
			if e = c.waitReplacement(ctx, created.ID); e != nil {
				return e
			}
		}
		return nil
	}()
	if result != nil {
		if _, storageErr := c.snapshot(); storageErr != nil {
			return storageErr
		}
		recovery, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if e = c.rollback(recovery, op, old, created.ID); e != nil {
			return errors.New("Replacement failed and recovery needs review. Inspect the original and replacement IDs, network attachments and restart policy before any retry")
		}
		return errors.New("Replacement failed; original container and network/restart settings restored. Existing volumes retained")
	}
	// Remove only the stopped immutable old ID, never its volumes or image.
	if e = c.mutate(ctx, "removing_previous", "DELETE", "/containers/"+old.ID+"?v=false&force=false", nil, nil); e != nil {
		return errors.New("Replacement is ready but previous container removal needs review; do not retry the update")
	}
	return nil
}
func (c *containerManagement) waitReplacement(ctx context.Context, id string) error {
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	stable := 0
	for {
		v, e := c.inspect(ctx, id)
		if e != nil {
			return e
		}
		if !v.State.Running || v.State.Restarting {
			return errors.New("Replacement did not stay running")
		}
		if v.State.Health != nil {
			if v.State.Health.Status == "healthy" {
				return nil
			}
			if v.State.Health.Status == "unhealthy" {
				return errors.New("Replacement health check failed")
			}
		} else {
			stable++
			if stable >= 3 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Replacement readiness timed out")
		case <-time.After(time.Second):
		}
	}
}
func (c *containerManagement) rollback(ctx context.Context, op containerOperation, old inspectedContainer, newID string) error {
	v, e := c.inspect(ctx, newID)
	if e != nil {
		return e
	}
	if value[map[string]string](v.Config, "Labels")["io.reefhq.replacement"] != op.ID {
		return errors.New("Replacement identity changed")
	}
	if v.State.Running {
		if e = c.mutate(ctx, "rollback_stop", "POST", "/containers/"+newID+"/stop?t=20", nil, nil); e != nil {
			return e
		}
	}
	if e = c.mutate(ctx, "rollback_remove", "DELETE", "/containers/"+newID+"?v=false&force=false", nil, nil); e != nil {
		return e
	}
	v, e = c.inspect(ctx, old.ID)
	if e != nil {
		return e
	}
	if v.Name != old.Name {
		if e = c.mutate(ctx, "rollback_name", "POST", "/containers/"+old.ID+"/rename?name="+url.QueryEscape(strings.TrimPrefix(old.Name, "/")), nil, nil); e != nil {
			return e
		}
	}
	plan, e := containerCreatePlan(old, old.Image, op.ID)
	if e != nil {
		return e
	}
	netcfg := value[struct {
		EndpointsConfig map[string]map[string]json.RawMessage
	}](plan, "NetworkingConfig")
	for name, ep := range netcfg.EndpointsConfig {
		if _, ok := v.NetworkSettings.Networks[name]; !ok {
			if e = c.mutate(ctx, "rollback_network", "POST", "/networks/"+url.PathEscape(name)+"/connect", map[string]any{"Container": old.ID, "EndpointConfig": ep}, nil); e != nil {
				return e
			}
		}
	}
	if e = c.mutate(ctx, "rollback_restart_policy", "POST", "/containers/"+old.ID+"/update", map[string]any{"RestartPolicy": value[map[string]any](old.HostConfig, "RestartPolicy")}, nil); e != nil {
		return e
	}
	if old.State.Running && !v.State.Running {
		if e = c.mutate(ctx, "rollback_start", "POST", "/containers/"+old.ID+"/start", nil, nil); e != nil {
			return e
		}
	}
	v, e = c.inspect(ctx, old.ID)
	if e != nil || v.Name != old.Name || v.State.Running != old.State.Running {
		return errors.New("Original state requires review")
	}
	type restartPolicy struct {
		Name              string
		MaximumRetryCount int
	}
	if value[restartPolicy](v.HostConfig, "RestartPolicy") != value[restartPolicy](old.HostConfig, "RestartPolicy") {
		return errors.New("Original restart policy requires review")
	}
	for name := range old.NetworkSettings.Networks {
		if _, ok := v.NetworkSettings.Networks[name]; !ok {
			return errors.New("Original network attachments require review")
		}
	}
	return c.checkpoint("rolled_back")
}
