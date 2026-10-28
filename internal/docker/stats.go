package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Stats exposes only derived metrics and counters, never raw Docker responses.
// Nil distinguishes an unsupported metric from a measured zero.
type Stats struct {
	Read          time.Time `json:"read"`
	CPUPercent    *float64  `json:"cpu_percent"`
	CPUInterval   float64   `json:"cpu_interval_seconds"`
	MemoryBytes   *uint64   `json:"memory_bytes"`
	MemoryLimit   *uint64   `json:"memory_limit_bytes"`
	MemoryPercent *float64  `json:"memory_percent"`
	NetworkRX     *uint64   `json:"network_rx_bytes"`
	NetworkTX     *uint64   `json:"network_tx_bytes"`
	BlockRead     *uint64   `json:"block_read_bytes"`
	BlockWrite    *uint64   `json:"block_write_bytes"`
}
type StatsReader interface {
	ReadStats(context.Context, string) (Stats, error)
}
type cpuSnapshot struct {
	CPUUsage struct {
		Total  uint64   `json:"total_usage"`
		PerCPU []uint64 `json:"percpu_usage"`
	} `json:"cpu_usage"`
	System uint64 `json:"system_cpu_usage"`
	Online uint64 `json:"online_cpus"`
}
type rawStats struct {
	ID      string      `json:"id"`
	OS      string      `json:"os_type"`
	Read    time.Time   `json:"read"`
	PreRead time.Time   `json:"preread"`
	CPU     cpuSnapshot `json:"cpu_stats"`
	PreCPU  cpuSnapshot `json:"precpu_stats"`
	Memory  struct {
		Usage *uint64           `json:"usage"`
		Limit *uint64           `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RX uint64 `json:"rx_bytes"`
		TX uint64 `json:"tx_bytes"`
	} `json:"networks"`
	Block struct {
		Rows []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
}

func (c *Client) ReadStats(ctx context.Context, id string) (Stats, error) {
	if !ValidContainerID(id) {
		return Stats{}, errors.New("Invalid container identity")
	}
	// stream=false waits for two sampling cycles for meaningful CPU deltas.
	// No follow stream, no persistent daemon subscription and no one-shot zero CPU.
	req, err := http.NewRequestWithContext(ctx, "GET", "http://docker/containers/"+id+"/stats?stream=false", nil)
	if err != nil {
		return Stats{}, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return Stats{}, errors.New("Cannot reach Docker to read statistics")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return Stats{}, errors.New("Container statistics unavailable; the container may have stopped or disappeared")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	var frame rawStats
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &frame) != nil || frame.ID != id || frame.Read.IsZero() || frame.Read.After(time.Now().Add(time.Minute)) || (frame.OS != "" && frame.OS != "linux") {
		return Stats{}, errors.New("Docker returned unavailable or invalid statistics")
	}
	return deriveStats(frame), nil
}
func deriveStats(f rawStats) Stats {
	out := Stats{Read: f.Read}
	cpus := f.CPU.Online
	if cpus == 0 {
		cpus = uint64(len(f.CPU.CPUUsage.PerCPU))
	}
	if cpus > 0 && cpus <= 1048576 && f.PreCPU.System > 0 && f.CPU.System > f.PreCPU.System && f.CPU.CPUUsage.Total >= f.PreCPU.CPUUsage.Total && f.PreRead.Before(f.Read) && !f.PreRead.IsZero() {
		percent := float64(f.CPU.CPUUsage.Total-f.PreCPU.CPUUsage.Total) / float64(f.CPU.System-f.PreCPU.System) * float64(cpus) * 100
		out.CPUPercent = &percent
		out.CPUInterval = f.Read.Sub(f.PreRead).Seconds()
	}
	if f.Memory.Usage != nil {
		used := *f.Memory.Usage
		// Match Linux docker stats: cgroup v1, cgroup v2, then legacy Docker.
		cache, ok := f.Memory.Stats["total_inactive_file"]
		if !ok {
			cache, ok = f.Memory.Stats["inactive_file"]
		}
		if !ok {
			cache = f.Memory.Stats["cache"]
		}
		if cache < used {
			used -= cache
		}
		out.MemoryBytes = &used
		if f.Memory.Limit != nil && *f.Memory.Limit > 0 {
			limit := *f.Memory.Limit
			out.MemoryLimit = &limit
			percent := float64(used) / float64(limit) * 100
			out.MemoryPercent = &percent
		}
	}
	if f.Networks != nil {
		var rx, tx uint64
		valid := true
		for _, n := range f.Networks {
			if ^uint64(0)-rx < n.RX || ^uint64(0)-tx < n.TX {
				valid = false
				break
			}
			rx += n.RX
			tx += n.TX
		}
		if valid && len(f.Networks) > 0 {
			out.NetworkRX = &rx
			out.NetworkTX = &tx
		}
	}
	if f.Block.Rows != nil {
		var read, write uint64
		valid := true
		for _, row := range f.Block.Rows {
			switch strings.ToLower(row.Op) {
			case "read":
				if ^uint64(0)-read < row.Value {
					valid = false
				} else {
					read += row.Value
				}
			case "write":
				if ^uint64(0)-write < row.Value {
					valid = false
				} else {
					write += row.Value
				}
			}
		}
		if valid {
			out.BlockRead = &read
			out.BlockWrite = &write
		}
	}
	return out
}
