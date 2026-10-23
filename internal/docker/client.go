// Package docker reads a minimal inventory from the local Docker Unix socket.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

type Container struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Image          string `json:"image"`
	State          string `json:"state"`
	Status         string `json:"status"`
	ComposeProject string `json:"compose_project"`
	ComposeService string `json:"compose_service"`
	WorkingDir     string `json:"-"`
	ConfigFiles    string `json:"-"`
	Reef           bool   `json:"-"`
}
type Lister interface {
	List(context.Context) ([]Container, error)
}
type Client struct{ http *http.Client }

func NewClient(socket string) *Client {
	return &Client{http: &http.Client{Timeout: 6 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
		MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second,
	}}}
}
func (c *Client) List(ctx context.Context) ([]Container, error) {
	request, e := http.NewRequestWithContext(ctx, "GET", "http://docker/containers/json?all=1", nil)
	if e != nil {
		return nil, e
	}
	response, e := c.http.Do(request)
	if e != nil {
		return nil, fmt.Errorf("Cannot reach the local Docker socket. Check Docker and Reef's socket mount.")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("Docker inventory is unavailable (HTTP %d).", response.StatusCode)
	}
	// Never inspect container environments, commands, files or arbitrary labels.
	var raw []struct {
		ID                   string `json:"Id"`
		Names                []string
		Image, State, Status string
		Labels               map[string]string
	}
	b, e := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if e != nil {
		return nil, fmt.Errorf("Cannot read Docker inventory.")
	}
	if e = json.Unmarshal(b, &raw); e != nil {
		return nil, fmt.Errorf("Docker returned an invalid container inventory.")
	}
	containers := make([]Container, 0, len(raw))
	for _, r := range raw {
		name := r.ID
		if len(r.Names) > 0 {
			name = strings.TrimPrefix(r.Names[0], "/")
		}
		containers = append(containers, Container{ID: r.ID, Name: name, Image: r.Image, State: r.State, Status: r.Status, ComposeProject: r.Labels["com.docker.compose.project"], ComposeService: r.Labels["com.docker.compose.service"], WorkingDir: hint(r.Labels["com.docker.compose.project.working_dir"]), ConfigFiles: hint(r.Labels["com.docker.compose.project.config_files"]), Reef: r.Labels["io.reef.local"] != "" || r.Labels["io.reef.instance"] == "true"})
	}
	sort.Slice(containers, func(i, j int) bool { return strings.ToLower(containers[i].Name) < strings.ToLower(containers[j].Name) })
	return containers, nil
}

// Hints are untrusted metadata, never file instructions. Do not return oversized labels.
func hint(s string) string {
	if len(s) > 8192 {
		return ""
	}
	return s
}
