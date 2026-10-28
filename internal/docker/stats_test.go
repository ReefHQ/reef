package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func statsFrame() rawStats {
	var f rawStats
	json.Unmarshal([]byte(`{"cpu_stats":{"cpu_usage":{"total_usage":300,"percpu_usage":[1,2]},"system_cpu_usage":2000,"online_cpus":2},"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000},"memory_stats":{"usage":1000,"limit":2000,"stats":{"total_inactive_file":200}},"networks":{"eth0":{"rx_bytes":100,"tx_bytes":200},"eth1":{"rx_bytes":50,"tx_bytes":70}},"blkio_stats":{"io_service_bytes_recursive":[{"op":"Read","value":10},{"op":"Write","value":20},{"op":"Read","value":5},{"op":"Total","value":35}]}}`), &f)
	f.ID = strings.Repeat("a", 64)
	f.Read = time.Now().UTC()
	f.PreRead = f.Read.Add(-time.Second)
	return f
}
func TestStatsDeriveLinuxCPUMemoryAndCounters(t *testing.T) {
	f := statsFrame()
	s := deriveStats(f)
	if s.CPUPercent == nil || *s.CPUPercent != 40 || s.CPUInterval != 1 || *s.MemoryBytes != 800 || *s.MemoryPercent != 40 || *s.NetworkRX != 150 || *s.NetworkTX != 270 || *s.BlockRead != 15 || *s.BlockWrite != 20 {
		t.Fatal(s)
	}
	for _, mode := range []string{"v2", "legacy", "missing-cache", "oversized-cache"} {
		f := statsFrame()
		f.CPU.Online = 0
		f.Memory.Stats = map[string]uint64{}
		want := uint64(800)
		switch mode {
		case "v2":
			f.Memory.Stats["inactive_file"] = 200
		case "legacy":
			f.Memory.Stats["cache"] = 200
		case "missing-cache":
			want = 1000
		case "oversized-cache":
			f.Memory.Stats["inactive_file"] = 2000
			want = 1000
		}
		s := deriveStats(f)
		if *s.MemoryBytes != want || *s.CPUPercent != 40 {
			t.Fatal(mode, s)
		}
	}
	f.CPU.CPUUsage.Total = 1100
	s = deriveStats(f)
	if *s.CPUPercent != 200 {
		t.Fatal("multi-core CPU was clamped", s)
	}
}
func TestStatsUnavailableMetricsAndCounterResetNeverBecomeZero(t *testing.T) {
	f := rawStats{Read: time.Now()}
	s := deriveStats(f)
	if s.CPUPercent != nil || s.MemoryBytes != nil || s.MemoryLimit != nil || s.NetworkRX != nil || s.BlockRead != nil {
		t.Fatal(s)
	}
	f = statsFrame()
	f.CPU.CPUUsage.Total = 10
	s = deriveStats(f)
	if s.CPUPercent != nil {
		t.Fatal("CPU underflow", s)
	}
	f = statsFrame()
	f.CPU.System = f.PreCPU.System
	s = deriveStats(f)
	if s.CPUPercent != nil {
		t.Fatal("zero denominator", s)
	}
	f = statsFrame()
	f.PreRead = time.Time{}
	if deriveStats(f).CPUPercent != nil {
		t.Fatal("missing interval accepted")
	}
	f = statsFrame()
	for key, n := range f.Networks {
		n.RX = ^uint64(0)
		f.Networks[key] = n
	}
	s = deriveStats(f)
	if s.NetworkRX != nil || s.NetworkTX != nil {
		t.Fatal("network counter overflow", s)
	}
	f = statsFrame()
	f.Block.Rows[0].Value = ^uint64(0)
	s = deriveStats(f)
	if s.BlockRead != nil {
		t.Fatal("block counter overflow", s)
	}
}
func TestStatsReadIsBoundedNonStreamingAndSanitized(t *testing.T) {
	id := strings.Repeat("a", 64)
	frame, _ := json.Marshal(statsFrame())
	for _, tc := range []struct {
		name, body string
		status     int
		fail       bool
	}{
		{"valid", string(frame), 200, false}, {"invalid", "private-json-secret", 200, false}, {"oversize", strings.Repeat("x", (1<<20)+1), 200, false}, {"daemon", "private-daemon-secret", 500, false}, {"transport", "", 200, true}, {"identity", strings.Replace(string(frame), id, strings.Repeat("b", 64), 1), 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := &Client{http: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/containers/"+id+"/stats" || r.URL.Query().Get("stream") != "false" || r.URL.Query().Has("one-shot") || r.Method != "GET" {
					t.Fatal(r.URL)
				}
				if tc.fail {
					return nil, errors.New("private-transport-secret")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			s, err := c.ReadStats(context.Background(), id)
			if tc.name == "valid" {
				if err != nil || s.CPUPercent == nil {
					t.Fatal(s, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "private-") {
				t.Fatal(err)
			}
			if _, err = c.ReadStats(context.Background(), "../secret"); err == nil || calls != 1 {
				t.Fatal("invalid ID reached Docker")
			}
		})
	}
}
