package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const ControlCapability = "control-v1"
const ImagesCapability = "images-v1"
const ImageBatchCapability = "images-batch-v1"
const VolumesCapability = "volumes-v1"
const MaxCommandBytes = 4 << 20
const MaxFrameBytes = 16 << 10

var ErrRelay = errors.New("Agent disconnected or command outcome unknown; refresh status before retrying")

// Only Reef operations are transported. No arbitrary URL, headers, Docker API,
// shell command, filesystem path or browser authentication can be dispatched.
func AllowedCommand(method, path string) bool {
	switch path {
	case "/api/volumes", "/api/volumes/operation", "/api/images", "/api/images/operation", "/api/state", "/api/document", "/api/containers", "/api/container-logs", "/api/projects/proposal":
		return method == "GET"
	case "/api/volumes/action", "/api/images/action", "/api/action", "/api/image-updates", "/api/projects/register", "/api/projects/unmanage", "/api/console/open", "/api/console/input", "/api/console/resize", "/api/console/close":
		return method == "POST"
	}
	return false
}

type Command struct {
	ID      string     `json:"id"`
	Owner   string     `json:"owner"`
	Expires time.Time  `json:"expires"`
	Method  string     `json:"method"`
	Path    string     `json:"path"`
	Query   url.Values `json:"query,omitempty"`
	Form    url.Values `json:"form,omitempty"`
}
type Frame struct {
	ID          string `json:"id"`
	Sequence    uint64 `json:"sequence"`
	Status      int    `json:"status,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Field       string `json:"field,omitempty"`
	Data        []byte `json:"data,omitempty"`
	Done        bool   `json:"done,omitempty"`
}
type Commands struct {
	Commands []Command `json:"commands"`
	Cancel   []string  `json:"cancel"`
}
type relayJob struct {
	cmd       Command
	frames    chan Frame
	stop      chan struct{}
	seq       uint64
	bytes     int
	delivered bool
	finished  bool
	size      int
}
type relayHost struct {
	generation string
	jobs       map[string]*relayJob
	cancel     []string
	wake       chan struct{}
}
type Relay struct {
	mu    sync.Mutex
	hosts map[string]*relayHost
	total int
	bytes int
}

func NewRelay() *Relay { return &Relay{hosts: map[string]*relayHost{}} }
func (r *Relay) Connect(host, generation string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disconnect(host)
	r.hosts[host] = &relayHost{generation: generation, jobs: map[string]*relayJob{}, wake: make(chan struct{}, 1)}
}
func (r *Relay) disconnect(host string) {
	if h := r.hosts[host]; h != nil {
		for _, j := range h.jobs {
			close(j.stop)
			r.total--
			r.bytes -= j.size
		}
		delete(r.hosts, host)
	}
}
func (r *Relay) Disconnect(host string) { r.mu.Lock(); defer r.mu.Unlock(); r.disconnect(host) }
func (r *Relay) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for host := range r.hosts {
		r.disconnect(host)
	}
}
func notify(h *relayHost) {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}
func (r *Relay) cancel(host, id string) { r.mu.Lock(); defer r.mu.Unlock(); r.cancelLocked(host, id) }
func (r *Relay) cancelLocked(host, id string) {
	h := r.hosts[host]
	if h == nil {
		return
	}
	if j := h.jobs[id]; j != nil {
		delete(h.jobs, id)
		close(j.stop)
		r.total--
		r.bytes -= j.size
		if j.delivered && !j.finished {
			if len(h.cancel) >= 128 {
				r.disconnect(host)
				return
			}
			h.cancel = append(h.cancel, id)
			notify(h)
		}
	}
}
func validValues(values url.Values) bool {
	if len(values) > 24 {
		return false
	}
	for k, v := range values {
		if len(k) > 100 || len(v) > 8 {
			return false
		}
	}
	return len(values.Encode()) <= MaxCommandBytes
}
func (r *Relay) Do(ctx context.Context, host string, command Command) (*http.Response, error) {
	if !AllowedCommand(command.Method, command.Path) || !validValues(command.Query) || !validValues(command.Form) || !validHex(command.Owner) || time.Until(command.Expires) <= 0 || time.Until(command.Expires) > 12*time.Hour {
		return nil, ErrInvalid
	}
	command.ID = secret()
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(command.Expires) {
		command.Expires = deadline
	}
	wire, _ := json.Marshal(command)
	if len(wire) > 7<<20 {
		return nil, ErrInvalid
	}
	j := &relayJob{cmd: command, frames: make(chan Frame, 16), stop: make(chan struct{})}
	r.mu.Lock()
	h := r.hosts[host]
	if h == nil || len(h.jobs) >= 24 || r.total >= 64 || r.bytes+len(wire) > 32<<20 {
		r.mu.Unlock()
		return nil, ErrRelay
	}
	j.size = len(wire)
	r.bytes += j.size
	h.jobs[command.ID] = j
	r.total++
	notify(h)
	r.mu.Unlock()
	reader := &relayBody{ctx: ctx, job: j, close: func() { r.cancel(host, command.ID) }}
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	var first Frame
	select {
	case first = <-j.frames:
	case <-j.stop:
		reader.Close()
		return nil, ErrRelay
	case <-ctx.Done():
		reader.Close()
		return nil, ctx.Err()
	case <-timer.C:
		reader.Close()
		return nil, ErrRelay
	}
	if first.Status < 100 || first.Status > 599 {
		reader.Close()
		return nil, ErrRelay
	}
	reader.pending = first.Data
	reader.done = first.Done
	header := http.Header{}
	contentType := "text/plain; charset=utf-8"
	if first.ContentType == "application/json" || first.ContentType == "application/x-ndjson" {
		contentType = first.ContentType
	}
	header.Set("Content-Type", contentType)
	if first.Field != "" {
		header.Set("X-Reef-Field", first.Field)
	}
	return &http.Response{StatusCode: first.Status, Header: header, Body: reader}, nil
}

type relayBody struct {
	ctx     context.Context
	job     *relayJob
	close   func()
	once    sync.Once
	pending []byte
	done    bool
}

func (b *relayBody) Read(p []byte) (int, error) {
	for len(b.pending) == 0 {
		if b.done {
			return 0, io.EOF
		}
		select {
		case frame := <-b.job.frames:
			b.pending = frame.Data
			b.done = frame.Done
		case <-b.job.stop:
			return 0, ErrRelay
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}
func (b *relayBody) Close() error { b.once.Do(b.close); return nil }
func (r *Relay) Poll(ctx context.Context, host, generation string) (Commands, error) {
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		r.mu.Lock()
		h := r.hosts[host]
		if h == nil || h.generation != generation {
			r.mu.Unlock()
			return Commands{}, ErrInvalid
		}
		result := Commands{Commands: []Command{}, Cancel: append([]string{}, h.cancel...)}
		h.cancel = nil
		wireBytes := 0
		for _, j := range h.jobs {
			if wireBytes+j.size > 7<<20 {
				continue
			}
			if !j.delivered && len(result.Commands) < 4 {
				j.delivered = true
				wireBytes += j.size
				result.Commands = append(result.Commands, j.cmd)
			}
		}
		wake := h.wake
		r.mu.Unlock()
		if len(result.Commands) > 0 || len(result.Cancel) > 0 {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return Commands{}, ctx.Err()
		case <-deadline.C:
			return result, nil
		case <-wake:
		}
	}
}
func (r *Relay) Results(host, generation string, frames []Frame) error {
	if len(frames) > 16 {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.hosts[host]
	if h == nil || h.generation != generation {
		return ErrInvalid
	}
	for _, frame := range frames {
		j := h.jobs[frame.ID]
		if j == nil {
			continue
		}
		if j.finished || !j.delivered || len(frame.Data) > MaxFrameBytes || frame.Sequence != j.seq+1 || len(frame.ContentType) > 100 || len(frame.Field) > 100 || j.seq == 0 && (frame.Status < 100 || frame.Status > 599) || j.seq > 0 && frame.Status != 0 {
			return ErrInvalid
		}
		j.seq++
		if frame.Done {
			j.finished = true
		}
		j.bytes += len(frame.Data)
		limit := 16 << 20
		if j.cmd.Path == "/api/console/open" {
			limit = 24 << 20
		}
		if j.bytes > limit {
			r.cancelLocked(host, frame.ID)
			return ErrInvalid
		}
		select {
		case j.frames <- frame:
		default:
			go r.cancel(host, frame.ID)
			return nil
		}
	}
	return nil
}
func validHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}
