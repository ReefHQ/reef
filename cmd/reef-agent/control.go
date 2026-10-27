package main

import (
	"context"
	"errors"
	"net/http"
	"reef/internal/agents"
	"reef/internal/app"
	"sync"
	"time"
)

// The command channel is outbound HTTPS long polling, independent from inventory.
// A delivered command is executed once. Broken connections cancel consoles, while
// already accepted Compose workers continue and remain queryable in agent state.
func (c *client) control(ctx context.Context, backend *app.AgentBackend, generation string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	running := map[string]context.CancelFunc{}
	completed := map[string]time.Time{}
	output := make(chan agents.Frame, 128)
	failures := make(chan error, 2)
	var workers sync.WaitGroup
	var network sync.WaitGroup
	network.Add(2)
	defer func() {
		cancel()
		network.Wait()
		mu.Lock()
		for _, stop := range running {
			stop()
		}
		mu.Unlock()
		workers.Wait()
		backend.CloseConsoles()
	}()
	go func() {
		defer network.Done()
		for ctx.Err() == nil {
			var batch []agents.Frame
			select {
			case <-ctx.Done():
				return
			case frame := <-output:
				batch = append(batch, frame)
			}
			for len(batch) < 8 {
				select {
				case frame := <-output:
					batch = append(batch, frame)
				default:
					goto send
				}
			}
		send:
			if err := c.post(ctx, "results", map[string]any{"generation": generation, "frames": batch}, nil); err != nil {
				select {
				case failures <- err:
				default:
				}
				cancel()
				return
			}
		}
	}()
	go func() {
		defer network.Done()
		for ctx.Err() == nil {
			var batch agents.Commands
			if err := c.post(ctx, "commands", map[string]string{"generation": generation}, &batch); err != nil {
				select {
				case failures <- err:
				default:
				}
				cancel()
				return
			}
			if len(batch.Commands) > 4 || len(batch.Cancel) > 128 {
				select {
				case failures <- errors.New("invalid command batch"):
				default:
				}
				cancel()
				return
			}
			mu.Lock()
			for id, expiry := range completed {
				if time.Now().After(expiry) {
					delete(completed, id)
				}
			}
			for _, id := range batch.Cancel {
				if stop := running[id]; stop != nil {
					stop()
				}
			}
			for _, command := range batch.Commands {
				if ctx.Err() != nil {
					break
				}
				_, done := completed[command.ID]
				if _, ok := running[command.ID]; ok || done {
					continue
				}
				if len(running) >= 32 || len(completed) >= 100000 || len(command.ID) != 64 || !agents.AllowedCommand(command.Method, command.Path) || len(command.Form.Encode()) > agents.MaxCommandBytes || len(command.Query.Encode()) > agents.MaxCommandBytes {
					cancel()
					break
				}
				deadline := command.Expires
				if maximum := time.Now().Add(30 * time.Minute); deadline.After(maximum) {
					deadline = maximum
				}
				job, stop := context.WithDeadline(ctx, deadline)
				running[command.ID] = stop
				workers.Add(1)
				go func(command agents.Command, job context.Context, stop context.CancelFunc) {
					defer workers.Done()
					defer stop()
					writer := &agentWriter{ctx: job, id: command.ID, output: output, header: http.Header{}}
					func() {
						defer func() {
							if recover() != nil {
								http.Error(writer, "Agent operation failed", 500)
							}
							writer.finish()
						}()
						backend.Serve(job, writer, command)
					}()
					mu.Lock()
					delete(running, command.ID)
					completed[command.ID] = command.Expires
					mu.Unlock()
				}(command, job, stop)
			}
			mu.Unlock()
		}
	}()
	select {
	case <-ctx.Done():
		select {
		case err := <-failures:
			return err
		default:
			return ctx.Err()
		}
	case err := <-failures:
		return err
	}
}

type agentWriter struct {
	ctx      context.Context
	id       string
	output   chan<- agents.Frame
	header   http.Header
	status   int
	sequence uint64
	deadline time.Time
	err      error
}

func (w *agentWriter) Header() http.Header { return w.header }
func (w *agentWriter) send(frame agents.Frame) error {
	if w.err != nil {
		return w.err
	}
	w.sequence++
	frame.ID = w.id
	frame.Sequence = w.sequence
	deadline := w.deadline
	if deadline.IsZero() {
		deadline = time.Now().Add(15 * time.Second)
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-w.ctx.Done():
		w.err = w.ctx.Err()
		return w.err
	case <-timer.C:
		w.err = errors.New("agent output backpressure timeout")
		return w.err
	case w.output <- frame:
		return nil
	}
}
func (w *agentWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	_ = w.send(agents.Frame{Status: status, ContentType: w.header.Get("Content-Type"), Field: w.header.Get("X-Reef-Field")})
}
func (w *agentWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	total := 0
	for len(data) > 0 {
		n := len(data)
		if n > agents.MaxFrameBytes {
			n = agents.MaxFrameBytes
		}
		if err := w.send(agents.Frame{Data: append([]byte(nil), data[:n]...)}); err != nil {
			return total, err
		}
		data = data[n:]
		total += n
	}
	return total, nil
}
func (w *agentWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
}
func (w *agentWriter) SetWriteDeadline(deadline time.Time) error { w.deadline = deadline; return nil }
func (w *agentWriter) finish() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	_ = w.send(agents.Frame{Done: true})
}
