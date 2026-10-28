package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reef/internal/monitoring"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMonitorReaderWhitelistBoundsAndUnavailableFields(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, mode := range []string{"healthy", "no-health", "missing-count", "wrong-id", "oversized", "error"} {
		t.Run(mode, func(t *testing.T) {
			body := `{"Id":"` + id + `","RestartCount":4,"State":{"Status":"running","Health":{"Status":"unhealthy","Log":[{"Output":"private-health-output"}]}},"Config":{"Env":["PASSWORD=private"]}}`
			switch mode {
			case "no-health":
				body = `{"Id":"` + id + `","RestartCount":0,"State":{"Status":"running"}}`
			case "missing-count":
				body = `{"Id":"` + id + `","State":{"Status":"running"}}`
			case "wrong-id":
				body = strings.Replace(body, id, strings.Repeat("b", 64), 1)
			case "oversized":
				body = strings.Repeat(" ", 1<<20) + body
			}
			client := &Client{http: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.URL.Path != "/containers/"+id+"/json" {
					t.Fatal(r.URL)
				}
				status := 200
				if mode == "error" {
					status = 500
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			value, err := client.ReadMonitor(context.Background(), id)
			if mode == "healthy" || mode == "no-health" {
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(value)
				if strings.Contains(string(raw), "private") {
					t.Fatal(string(raw))
				}
			} else if err == nil {
				t.Fatal("invalid inspection accepted")
			}
		})
	}
}

type monitorFixture struct {
	rows            []Container
	active, maximum atomic.Int32
}

func (f *monitorFixture) List(context.Context) ([]Container, error) { return f.rows, nil }
func (f *monitorFixture) ReadMonitor(ctx context.Context, id string) (monitoring.Container, error) {
	n := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		old := f.maximum.Load()
		if n <= old || f.maximum.CompareAndSwap(old, n) {
			break
		}
	}
	select {
	case <-ctx.Done():
		return monitoring.Container{}, ctx.Err()
	case <-time.After(time.Millisecond):
	}
	if id == strings.Repeat("b", 64) {
		return monitoring.Container{}, errors.New("private-daemon-error")
	}
	return monitoring.Container{ID: id, State: "running", Health: "healthy"}, nil
}
func TestMonitorCollectionConcurrencyFailureAndLimits(t *testing.T) {
	f := &monitorFixture{}
	for _, c := range "abcdef123456" {
		f.rows = append(f.rows, Container{ID: strings.Repeat(string(c), 64), Name: "app", State: "running"})
	}
	s := CollectMonitor(context.Background(), f, f)
	if !s.Valid() || f.maximum.Load() > 4 {
		t.Fatal(s, f.maximum.Load())
	}
	failed := 0
	for _, r := range s.Containers {
		if r.Error {
			failed++
		}
	}
	if failed != 1 {
		t.Fatal(s)
	}
	f.rows = make([]Container, 1001)
	if CollectMonitor(context.Background(), f, f).Error != "Container inventory exceeds monitoring limit" {
		t.Fatal("limit ignored")
	}
}
