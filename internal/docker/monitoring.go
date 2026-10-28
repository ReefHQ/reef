package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reef/internal/monitoring"
	"reef/internal/notifications"
	"sync"
)

type MonitorReader interface {
	ReadMonitor(context.Context, string) (monitoring.Container, error)
}

func (c *Client) ReadMonitor(ctx context.Context, id string) (monitoring.Container, error) {
	out := monitoring.Container{ID: id}
	fail := func() (monitoring.Container, error) { return out, errors.New("Container observation unavailable") }
	if !ValidContainerID(id) {
		return fail()
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker/containers/"+id+"/json", nil)
	res, e := c.http.Do(req)
	if e != nil {
		return fail()
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fail()
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return fail()
	}
	// Decode only status/counters; health command output, errors, env and config
	// never leave this reader. No exec or streaming Docker subscription is used.
	var v struct {
		ID           string `json:"Id"`
		RestartCount *int64
		State        struct {
			Status string
			Health *struct{ Status string }
		}
	}
	if json.Unmarshal(raw, &v) != nil || v.ID != id || v.RestartCount == nil || *v.RestartCount < 0 {
		return fail()
	}
	out.State = v.State.Status
	out.Restarts = *v.RestartCount
	if v.State.Health != nil {
		out.Health = v.State.Health.Status
	}
	if !(monitoring.Sample{Sequence: 1, Containers: []monitoring.Container{out}}).Valid() {
		return fail()
	}
	return out, nil
}

// CollectMonitor is bounded to 1000 containers, four concurrent inspections and
// the caller's deadline. Failed rows remain explicit unknowns, not healthy zeros.
func CollectMonitor(ctx context.Context, l Lister, r MonitorReader) monitoring.Sample {
	sample := monitoring.Sample{Sequence: 1, Containers: []monitoring.Container{}}
	rows, e := l.List(ctx)
	if e != nil {
		sample.Error = "Collection unavailable"
		return sample
	}
	if len(rows) > monitoring.MaxContainers {
		sample.Error = "Container inventory exceeds monitoring limit"
		return sample
	}
	seen := map[string]bool{}
	for _, c := range rows {
		if !ValidContainerID(c.ID) || seen[c.ID] {
			sample.Error = "Collection unavailable"
			return sample
		}
		seen[c.ID] = true
	}
	sample.Containers = make([]monitoring.Container, len(rows))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for n := 0; n < min(4, len(rows)); n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				c := rows[i]
				item, err := r.ReadMonitor(ctx, c.ID)
				if err != nil {
					item = monitoring.Container{ID: c.ID, State: c.State, Error: true}
				}
				item.Name = notifications.Text(c.Name, 160)
				sample.Containers[i] = item
			}
		}()
	}
	for i := range rows {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return sample
}
