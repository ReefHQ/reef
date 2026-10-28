// Package monitoring evaluates bounded, read-only host observations centrally.
package monitoring

import (
	"errors"
	"regexp"
	"time"
)

const MaxContainers = 1000
const SampleInterval = 30 * time.Second
const MaxIncidents = 2000
const MaxPending = 256

var hexID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var episodeID = regexp.MustCompile(`^[a-f0-9]{48}$`)
var ErrState = errors.New("Monitoring persistence unavailable; inspect private monitoring state and restart Reef")

type Config struct {
	Enabled              bool `json:"enabled"`
	DisconnectSeconds    int  `json:"disconnect_seconds"`
	UnhealthySeconds     int  `json:"unhealthy_seconds"`
	RestartCount         int  `json:"restart_count"`
	RestartWindowSeconds int  `json:"restart_window_seconds"`
	RecoverySeconds      int  `json:"recovery_seconds"`
}

func Defaults() Config {
	return Config{DisconnectSeconds: 120, UnhealthySeconds: 120, RestartCount: 3, RestartWindowSeconds: 300, RecoverySeconds: 60}
}
func (c Config) Valid() bool {
	return c.DisconnectSeconds >= 60 && c.DisconnectSeconds <= 3600 && c.UnhealthySeconds >= 30 && c.UnhealthySeconds <= 3600 && c.RestartCount >= 2 && c.RestartCount <= 100 && c.RestartWindowSeconds >= 60 && c.RestartWindowSeconds <= 3600 && c.RecoverySeconds >= 30 && c.RecoverySeconds <= 600
}

type Container struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Health   string `json:"health"`
	Restarts int64  `json:"restarts"`
	Error    bool   `json:"error,omitempty"`
}
type Sample struct {
	Sequence    uint64      `json:"sequence"`
	Containers  []Container `json:"containers"`
	Maintenance bool        `json:"maintenance"`
	Error       string      `json:"error,omitempty"`
}

func (s Sample) Valid() bool {
	if s.Sequence == 0 || len(s.Containers) > MaxContainers || (s.Error != "" && s.Error != "Collection unavailable" && s.Error != "Container inventory exceeds monitoring limit") {
		return false
	}
	seen := map[string]bool{}
	for _, c := range s.Containers {
		if !hexID.MatchString(c.ID) || seen[c.ID] || len(c.Name) > 640 || c.Restarts < 0 {
			return false
		}
		seen[c.ID] = true
		switch c.State {
		case "created", "running", "paused", "restarting", "removing", "exited", "dead":
		default:
			return false
		}
		switch c.Health {
		case "", "starting", "healthy", "unhealthy":
		default:
			return false
		}
	}
	return true
}
func ValidHost(s string) bool { return s == "local" || hexID.MatchString(s) }

type Incident struct {
	ID       string    `json:"id"`
	HostID   string    `json:"host_id"`
	Host     string    `json:"host"`
	Kind     string    `json:"kind"`
	TargetID string    `json:"target_id"`
	Target   string    `json:"target"`
	Status   string    `json:"status"`
	Opened   time.Time `json:"opened"`
	Updated  time.Time `json:"updated"`
	Closed   time.Time `json:"closed,omitempty"`
	Note     string    `json:"note,omitempty"`
}
type Coverage struct {
	HostID     string    `json:"host_id"`
	Host       string    `json:"host"`
	Status     string    `json:"status"`
	Checked    time.Time `json:"checked_at"`
	Containers int       `json:"containers"`
	Failed     int       `json:"failed"`
	HoldUntil  time.Time `json:"hold_until,omitempty"`
}

func validKind(s string) bool { return s == "disconnected" || s == "unhealthy" || s == "restart_loop" }
