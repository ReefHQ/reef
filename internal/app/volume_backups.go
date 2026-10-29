package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reef/internal/volumebackup"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Helpers have no network, host binds, Docker socket or application secrets.
// The archive is streamed to the Docker client's filesystem, not a daemon path.
const backupHelperImage = "docker:29.9.2-cli"

type VolumeBackups struct {
	mu    sync.Mutex
	store *volumebackup.Store
	op    CatalogOperation
	err   error
	keep  int
}
type backupUser struct {
	ID        string                        `json:"id"`
	Name      string                        `json:"name"`
	Running   bool                          `json:"running"`
	Protected bool                          `json:"protected"`
	Project   string                        `json:"project"`
	Mounts    []struct{ Type, Name string } `json:"mounts"`
}
type backupPreview struct {
	Mountpoint  string       `json:"-"`
	Name        string       `json:"name"`
	Fingerprint string       `json:"fingerprint"`
	Usage       string       `json:"usage"`
	Containers  []backupUser `json:"containers"`
}

func sortBackupUsers(users []backupUser) {
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
}

const backupUsageFormat = `{"id":{{json .Id}},"name":{{json .Name}},"running":{{.State.Running}},"protected":{{if or (index .Config.Labels "io.reef.local") (eq (index .Config.Labels "io.reef.instance") "true")}}true{{else}}false{{end}},"project":{{json (index .Config.Labels "com.docker.compose.project")}},"mounts":[{{range $i,$m := .Mounts}}{{if $i}},{{end}}{"type":{{json $m.Type}},"name":{{json (index $m "Name")}}}{{end}}]}`

func (m *Manager) initVolumeBackups(c Config) {
	root := c.VolumeBackupDirectory
	if root == "" {
		root = filepath.Join(c.StateDir, "volume-backups")
	}
	keep := c.VolumeBackupRetention
	if keep == 0 {
		keep = 7
	}
	b := &VolumeBackups{keep: keep}
	b.store, b.err = volumebackup.Open(root, keep)
	m.Backups = b
	if b.err == nil {
		rows, e := b.store.List()
		if e != nil {
			b.err = e
			return
		}
		for _, r := range rows {
			if r.Status == "pending" {
				r.Status = "interrupted"
				r.Message = "Interrupted. Check the listed container IDs and restart any stopped applications manually before retrying."
				if r.Destination != "" {
					r.Message = "Restoration interrupted; inspect new volume " + r.Destination + " before use. No automatic replay."
				}
				if e = b.store.Save(r); e != nil {
					b.err = e
					return
				}
			}
		}
	}
}
func (b *VolumeBackups) operation() CatalogOperation {
	b.mu.Lock()
	defer b.mu.Unlock()
	o := b.op
	o.Results = append([]ImageResult{}, o.Results...)
	return o
}
func (s *ImageService) backupPreview(ctx context.Context, name, fingerprint string) (backupPreview, error) {
	p := backupPreview{Containers: []backupUser{}}
	rows, e := s.volumes(ctx)
	if e != nil {
		return p, e
	}
	found := false
	for _, r := range rows {
		if r.Name == name && r.Fingerprint == fingerprint {
			found = true
		}
	}
	if !found {
		return p, errors.New("Volume identity changed; refresh and review")
	}
	raw, e := s.volumeCommand(ctx, "volume", "inspect", "--format", volumeFormat, name)
	var meta volumeMetadata
	if e != nil || json.Unmarshal(raw, &meta) != nil || volumeFingerprint(meta) != fingerprint || meta.Driver != "local" || meta.Scope != "local" || len(meta.Options) != 0 || meta.Created == "" {
		return p, errors.New("Backups require an ordinary local volume without driver options")
	}
	raw, e = s.volumeCommand(ctx, "container", "ls", "--all", "--quiet", "--no-trunc")
	if e != nil {
		return p, e
	}
	ids := strings.Fields(string(raw))
	if len(ids) > 10000 {
		return p, errors.New("Container inventory too large")
	}
	all := []backupUser{}
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		batch := map[string]bool{}
		for _, id := range ids[start:end] {
			if !containerIDPattern.MatchString(id) || batch[id] {
				return p, errors.New("Invalid container identity")
			}
			batch[id] = true
		}
		raw, e = s.volumeCommand(ctx, append([]string{"container", "inspect", "--format", backupUsageFormat}, ids[start:end]...)...)
		if e != nil {
			return p, e
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		for {
			var u backupUser
			e = d.Decode(&u)
			if e == io.EOF {
				break
			}
			if e != nil || !batch[u.ID] {
				return p, errors.New("Invalid container usage")
			}
			delete(batch, u.ID)
			u.Name = strings.TrimPrefix(u.Name, "/")
			all = append(all, u)
		}
		if len(batch) > 0 {
			return p, errors.New("Container usage changed")
		}
	}
	protected := map[string]bool{}
	for _, u := range all {
		if u.Protected && u.Project != "" {
			protected[u.Project] = true
		}
	}
	for _, u := range all {
		for _, v := range u.Mounts {
			if v.Type == "volume" && v.Name == name {
				if u.Protected || protected[u.Project] {
					return p, errors.New("Reef administration volumes cannot be backed up from Reef")
				}
				p.Containers = append(p.Containers, u)
				if len(p.Containers) > 100 {
					return p, errors.New("At most 100 container consumers may be stopped for one backup")
				}
				break
			}
		}
	}
	// Docker's list order may vary; the confirmation covers exact identities/state.
	sortBackupUsers(p.Containers)
	b, _ := json.Marshal(p.Containers)
	h := sha256.Sum256(b)
	p.Name = name
	p.Mountpoint = meta.Mountpoint
	p.Fingerprint = fingerprint
	p.Usage = hex.EncodeToString(h[:])
	return p, nil
}
func (s *Server) volumeBackups(w http.ResponseWriter, r *http.Request) {
	b := s.m.Backups
	if b == nil || b.err != nil {
		http.Error(w, "Private backup storage unavailable; check host configuration and directory permissions", 503)
		return
	}
	if r.Method == "GET" {
		rows, e := b.store.List()
		if e != nil {
			http.Error(w, e.Error(), 503)
			return
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset < 0 || offset > len(rows) {
			offset = 0
		}
		end := min(offset+50, len(rows))
		result := map[string]any{"backups": rows[offset:end], "total": len(rows), "offset": offset, "operation": b.operation(), "retention": b.keep}
		if name := r.URL.Query().Get("volume"); name != "" {
			p, e := s.m.Images.backupPreview(r.Context(), name, r.URL.Query().Get("fingerprint"))
			if e != nil {
				http.Error(w, e.Error(), 409)
				return
			}
			result["preview"] = p
		}
		writeJSON(w, result)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if r.Form.Get("confirmed") != "yes" {
		http.Error(w, "Explicit confirmation required", 400)
		return
	}
	op, e := s.m.beginVolumeBackup(r.Form.Get("action"), r.Form.Get("volume"), r.Form.Get("fingerprint"), r.Form.Get("usage"), r.Form.Get("backup"), r.Form.Get("name"))
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	writeJSON(w, op)
}
func (m *Manager) beginVolumeBackup(action, volume, fingerprint, usage, id, name string) (CatalogOperation, error) {
	fail := func(t string) (CatalogOperation, error) { return CatalogOperation{}, errors.New(t) }
	b := m.Backups
	if b == nil || b.err != nil || m.Images == nil {
		return fail("Backup storage unavailable")
	}
	if action == "backup" {
		if !volumeNamePattern.MatchString(volume) || !containerIDPattern.MatchString(fingerprint) || !containerIDPattern.MatchString(usage) {
			return fail("Refresh the volume backup preview")
		}
	} else if action == "restore" || action == "delete" {
		if len(id) != 48 {
			return fail("Invalid backup identity")
		}
		if _, e := hex.DecodeString(id); e != nil {
			return fail("Invalid backup identity")
		}
		if action == "restore" && (!volumeNamePattern.MatchString(name) || len(name) < 2) {
			return fail("Choose a new volume name, 2–255 letters, digits, dots, underscores or hyphens")
		}
	} else {
		return fail("Unsupported backup action")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storageBusy || m.agentUpdating {
		return fail("Wait for host operations to finish")
	}
	for _, p := range m.Projects {
		p.mu.Lock()
		busy := p.busy
		p.mu.Unlock()
		if busy {
			return fail("Wait for Compose operations to finish")
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	op := CatalogOperation{ID: randomID(), Action: action, Running: true, Total: 1, Started: time.Now(), Results: []ImageResult{}}
	b.op = op
	m.storageBusy = true
	m.wg.Add(1)
	target := volume
	if action == "restore" {
		target = name
	}
	m.catalogEvent("volume", backupEventAction(action), "started", op.ID, target, 1, nil)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		e := m.runVolumeBackup(ctx, action, volume, fingerprint, usage, id, name)
		result := ImageResult{ID: target, OK: e == nil, Message: "Completed"}
		if e != nil {
			result.Message = e.Error()
		}
		m.mu.Lock()
		b.mu.Lock()
		b.op.Results = []ImageResult{result}
		b.op.Running = false
		b.op.Finished = time.Now()
		m.storageBusy = false
		b.mu.Unlock()
		m.mu.Unlock()
		m.catalogEvent("volume", backupEventAction(action), "completed", op.ID, target, 1, []ImageResult{result})
	}()
	return op, nil
}
func backupEventAction(action string) string {
	if action == "delete" {
		return "backup_delete"
	}
	return action
}
func (m *Manager) runVolumeBackup(ctx context.Context, action, volume, fingerprint, usage, id, name string) error {
	b := m.Backups
	if action != "backup" {
		rows, e := b.store.List()
		if e != nil {
			return e
		}
		for _, r := range rows {
			if r.ID == id {
				if action == "delete" {
					return b.store.Delete(id)
				}
				p, e := b.store.Archive(r)
				if e != nil {
					return e
				}
				restore := volumebackup.Record{ID: randomID(), Volume: r.Volume, Fingerprint: r.Fingerprint, Created: time.Now().UTC(), Status: "pending", Containers: []string{}, Destination: name, Source: r.ID, Message: "Restoration in progress; do not attach applications to the new volume yet"}
				if e = b.store.Save(restore); e != nil {
					return e
				}
				e = m.restoreVolume(ctx, p, name, restore.ID)
				restore.Status = "succeeded"
				restore.Message = "Restored into new volume " + name + "; application configuration was not changed"
				if e != nil {
					restore.Status = "failed"
					restore.Message = "Restoration incomplete; inspect destination " + name + " before use"
				}
				if se := b.store.Save(restore); se != nil {
					return errors.New("Restore result could not be saved; inspect the destination before use")
				}
				return e
			}
		}
		return errors.New("Backup not found on this host")
	}
	p, e := m.Images.backupPreview(ctx, volume, fingerprint)
	if e != nil {
		return e
	}
	if p.Usage != usage {
		return errors.New("Container usage changed; review the backup confirmation again")
	}
	if p.Mountpoint != "" {
		rel, e := filepath.Rel(p.Mountpoint, b.store.Directory())
		if e == nil && (rel == "." || (!filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
			return errors.New("Backup storage must be outside the source volume")
		}
	}
	prior, e := b.store.List()
	if e != nil {
		return e
	}
	for _, v := range prior {
		if v.Status == "interrupted" && len(v.Containers) > 0 {
			return errors.New("Review interrupted backups and recover the listed containers, then delete their records before starting another backup")
		}
	}
	// Resolve/pull the helper before application downtime; no implicit helper upgrade during export.
	raw, e := m.Images.command(ctx, Project{}, "image", "inspect", "--format", "{{.Id}}", backupHelperImage)
	if e != nil {
		if _, e = m.backupLongCommand(ctx, "pull", backupHelperImage); e != nil {
			return errors.New("Could not prepare the backup helper image")
		}
		raw, e = m.Images.command(ctx, Project{}, "image", "inspect", "--format", "{{.Id}}", backupHelperImage)
	}
	image := strings.TrimSpace(string(raw))
	if e != nil || !imageIDPattern.MatchString(image) {
		return errors.New("Could not resolve the backup helper image")
	}
	r := volumebackup.Record{ID: randomID(), Volume: volume, Fingerprint: fingerprint, Created: time.Now().UTC(), Status: "pending", Containers: []string{}, Message: "Backup in progress"}
	// Archive identity uses Reef’s random 24-byte identifier.
	if e = b.store.Save(r); e != nil {
		return e
	}
	f, e := b.store.CreateArchive(r.ID)
	if e != nil {
		r.Status = "failed"
		r.Message = "Archive could not be created"
		_ = b.store.Save(r)
		return e
	}
	e = m.captureVolume(ctx, &r, p, image, f)
	syncErr := f.Sync()
	closeErr := f.Close()
	if e == nil {
		e = syncErr
	}
	if e == nil {
		e = closeErr
	}
	if e == nil {
		r.Bytes, r.SHA256, e = b.store.Finish(r.ID)
	}
	if e != nil {
		r.Status = "failed"
		r.Message = "Backup incomplete. Check container state; this archive cannot be restored."
	} else {
		r.Status = "succeeded"
		r.Message = "Verified archive; application consistency requires a clean shutdown"
	}
	if se := b.store.Save(r); se != nil {
		return errors.New("Backup result could not be persisted; inspect host state before retrying")
	}
	if e != nil {
		return e
	}
	if e = b.store.Retain(volume, fingerprint); e != nil {
		return errors.New("Backup saved, but retention cleanup failed; review stored archives")
	}
	return nil
}
func (m *Manager) captureVolume(ctx context.Context, r *volumebackup.Record, p backupPreview, image string, f *os.File) (result error) {
	latest, e := m.Images.backupPreview(ctx, p.Name, p.Fingerprint)
	if e != nil {
		return e
	}
	if latest.Usage != p.Usage {
		return errors.New("Container usage changed while preparing the helper; review the backup again")
	}
	// Every stop intent is durable before Docker receives it. Always attempt to
	// resume originally running containers, even after timeout or a failed stop.
	defer func() {
		for _, id := range r.Containers {
			c, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			_, e := m.Images.volumeCommand(c, "container", "start", id)
			if e == nil {
				raw, check := m.Images.volumeCommand(c, "container", "inspect", "--format", "{{.State.Running}}", id)
				if check != nil || strings.TrimSpace(string(raw)) != "true" {
					e = errors.New("Container did not stay running")
				}
			}
			cancel()
			if e != nil {
				result = errors.New("Backup did not complete safely: an application could not be restarted; check the recorded container IDs")
			}
		}
	}()
	for _, u := range p.Containers {
		if !u.Running {
			continue
		}
		r.Containers = append(r.Containers, u.ID)
		if e := m.Backups.store.Save(*r); e != nil {
			return e
		}
		if _, e := m.Images.volumeCommand(ctx, "container", "stop", "--time", "20", u.ID); e != nil {
			return errors.New("Container stop failed; backup aborted and restart attempted")
		}
		raw, e := m.Images.volumeCommand(ctx, "container", "inspect", "--format", "{{.State.ExitCode}}", u.ID)
		if e != nil || strings.TrimSpace(string(raw)) == "137" {
			return errors.New("Application shutdown was not clean; backup aborted and restart attempted")
		}
	}
	check, e := m.Images.backupPreview(ctx, p.Name, p.Fingerprint)
	if e != nil {
		return e
	}
	if len(check.Containers) != len(p.Containers) {
		return errors.New("Volume usage changed; backup aborted")
	}
	for i, u := range check.Containers {
		if u.Running || u.ID != p.Containers[i].ID {
			return errors.New("A container is still running or volume usage changed; backup aborted")
		}
	}
	helper := "reef-volume-backup-" + r.ID
	args := helperArgs(helper, image, p.Name, true)
	args = append(args, "-czpf", "-", "-C", "/data", ".")
	created, e := m.Images.command(ctx, Project{}, args...)
	if e != nil || !containerIDPattern.MatchString(strings.TrimSpace(string(created))) {
		return errors.New("Could not create isolated backup helper")
	}
	helper = strings.TrimSpace(string(created))
	defer func() {
		if e := m.removeBackupHelper(helper); e != nil {
			result = errors.New("Backup helper cleanup failed; inspect the named helper and container state")
		}
	}()
	// Creating the helper adds its own reference; recheck immutable volume identity.
	raw, e := m.Images.volumeCommand(ctx, "volume", "inspect", "--format", volumeFormat, p.Name)
	var meta volumeMetadata
	if e != nil || json.Unmarshal(raw, &meta) != nil || volumeFingerprint(meta) != p.Fingerprint {
		return errors.New("Volume changed before export")
	}
	run, ok := m.Exec.(interface {
		Output(context.Context, Project, []string, io.Writer) error
	})
	if !ok {
		return errors.New("Binary Docker output unavailable")
	}
	w := &backupLimitWriter{w: f, left: volumebackup.MaxArchive}
	if e = run.Output(ctx, Project{}, []string{"container", "start", "--attach", helper}, w); e != nil {
		return errors.New("Volume export failed or exceeded 20 GiB; partial archive retained for review")
	}
	if e = m.backupHelperExit(ctx, helper); e != nil {
		return e
	}
	return nil
}
func helperArgs(name, image, volume string, readOnly bool) []string {
	mount := "type=volume,src=" + volume + ",dst=/data,volume-nocopy"
	if readOnly {
		mount += ",readonly"
	}
	a := []string{"container", "create", "--name", name, "--label", "io.reef.instance=true", "--network", "none", "--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "256m", "--log-driver", "none", "--mount", mount}
	if readOnly {
		a = append(a, "--read-only")
	} else {
		a = append(a, "--cap-add", "CHOWN", "--cap-add", "FOWNER")
	}
	return append(a, "--entrypoint", "tar", image)
}
func (m *Manager) removeBackupHelper(name string) error {
	c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, e := m.Images.volumeCommand(c, "container", "rm", "--force", name)
	return e
}
func (m *Manager) backupHelperExit(ctx context.Context, name string) error {
	raw, e := m.Images.volumeCommand(ctx, "container", "inspect", "--format", "{{.State.ExitCode}}", name)
	if e != nil || strings.TrimSpace(string(raw)) != "0" {
		return errors.New("Archive helper failed; result is incomplete")
	}
	return nil
}

type backupLimitWriter struct {
	w    io.Writer
	left int64
}

func (w *backupLimitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.left {
		return 0, errors.New("Backup size limit reached")
	}
	n, e := w.w.Write(p)
	w.left -= int64(n)
	return n, e
}
func (m *Manager) restoreVolume(ctx context.Context, archive, name, identity string) (result error) {
	rows, e := m.Images.volumes(ctx)
	if e != nil {
		return e
	}
	for _, r := range rows {
		if r.Name == name {
			return errors.New("Restore requires a new volume name; existing data is never overwritten")
		}
	}
	// Reuse the already available image by ID. No extra registry dependency is
	// introduced after creating the destination.
	raw, e := m.Images.command(ctx, Project{}, "image", "inspect", "--format", "{{.Id}}", backupHelperImage)
	image := strings.TrimSpace(string(raw))
	if e != nil || !imageIDPattern.MatchString(image) {
		return errors.New("Backup helper image unavailable; prepare a backup on this host first")
	}
	if _, e = m.Images.volumeCommand(ctx, "volume", "create", "--driver", "local", "--label", "io.reefhq.restore="+identity, name); e != nil {
		return e
	}
	raw, e = m.Images.volumeCommand(ctx, "volume", "inspect", "--format", volumeFormat, name)
	var meta volumeMetadata
	if e != nil || json.Unmarshal(raw, &meta) != nil || meta.Labels["io.reefhq.restore"] != identity {
		return errors.New("Destination identity changed; no data restored")
	}
	fingerprint := volumeFingerprint(meta)
	helper := "reef-volume-restore-" + identity
	// docker cp writes into the stopped helper's private writable layer, so the
	// target volume remains inaccessible until archive validation has succeeded.
	args := helperArgs(helper, image, name, false)
	args = append(args, "--numeric-owner", "-xzpf", "/archive.tar.gz", "-C", "/data")
	created, e := m.Images.command(ctx, Project{}, args...)
	if e != nil || !containerIDPattern.MatchString(strings.TrimSpace(string(created))) {
		return fmt.Errorf("Restore helper creation failed; inspect new volume %s", name)
	}
	helperName := helper
	helper = strings.TrimSpace(string(created))
	defer func() {
		if e := m.removeBackupHelper(helper); e != nil {
			result = fmt.Errorf("Restore helper cleanup failed; inspect new volume %s and its helper", name)
		}
	}()
	if _, e = m.backupLongCommand(ctx, "cp", archive, helper+":/archive.tar.gz"); e != nil {
		return fmt.Errorf("Archive copy failed; inspect new volume %s", name)
	}
	raw, e = m.Images.volumeCommand(ctx, "volume", "inspect", "--format", volumeFormat, name)
	if e != nil || json.Unmarshal(raw, &meta) != nil || volumeFingerprint(meta) != fingerprint {
		return errors.New("Destination changed before restoration")
	}
	// Refuse consumers other than our helper, including stopped containers.
	rows, e = m.Images.volumes(ctx)
	if e != nil {
		return e
	}
	found := false
	for _, v := range rows {
		if v.Name == name {
			found = v.Fingerprint == fingerprint && len(v.Containers) == 1 && v.Containers[0] == helperName
		}
	}
	if !found {
		return errors.New("Destination gained another container; restoration aborted")
	}
	if _, e = m.backupLongCommand(ctx, "container", "start", "--attach", helper); e != nil {
		return fmt.Errorf("Restore incomplete; do not attach applications to %s before reviewing it", name)
	}
	if e = m.backupHelperExit(ctx, helper); e != nil {
		return fmt.Errorf("Restore incomplete; inspect new volume %s", name)
	}
	return nil
}

func (m *Manager) backupLongCommand(ctx context.Context, args ...string) ([]byte, error) {
	run, ok := m.Exec.(interface {
		Output(context.Context, Project, []string, io.Writer) error
	})
	if !ok {
		return nil, errors.New("Docker output unavailable")
	}
	return nil, run.Output(ctx, Project{}, args, io.Discard)
}
