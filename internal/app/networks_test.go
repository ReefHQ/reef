package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reef/internal/agents"
	"reef/internal/notifications"
	"strings"
	"sync"
	"testing"
	"time"
)

var networkA = strings.Repeat("1", 64)
var networkB = strings.Repeat("2", 64)
var networkC = strings.Repeat("3", 64)

type networkExecutor struct {
	mu            sync.Mutex
	rows          []networkMetadata
	containers    []NetworkContainer
	calls         [][]string
	fail          bool
	gate, entered chan struct{}
}

func networkFixture() *networkExecutor {
	return &networkExecutor{rows: []networkMetadata{
		{ID: networkA, Name: "app-net", Driver: "bridge", Scope: "local", Created: "2026-10-28", Labels: map[string]string{"com.docker.compose.project": "app", "private": "private-label-secret"}, Options: map[string]string{"password": "private-driver-secret"}, IPAM: json.RawMessage(`{"Driver":"default","Config":[{"Subnet":"172.30.0.0/16","Gateway":"172.30.0.1"}],"Options":{"password":"private-ipam-secret"}}`)},
		{ID: networkB, Name: "unused-net", Driver: "bridge", Scope: "local", IPAM: json.RawMessage(`{"Config":[]}`)},
		{ID: networkC, Name: "bridge", Driver: "bridge", Scope: "local", IPAM: json.RawMessage(`{}`)},
	}, containers: []NetworkContainer{}}
}
func (x *networkExecutor) Run(ctx context.Context, p Project, args []string, w io.Writer) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.calls = append(x.calls, append([]string{}, args...))
	if x.fail {
		io.WriteString(w, "private-cli-secret")
		return errors.New("private-cli-secret")
	}
	emit := func(value any) error { return json.NewEncoder(w).Encode(value) }
	switch strings.Join(args[:2], " ") {
	case "network ls":
		for _, row := range x.rows {
			io.WriteString(w, row.ID+"\n")
		}
		return nil
	case "network inspect":
		for _, id := range args[4:] {
			found := false
			for _, row := range x.rows {
				if row.ID == id {
					emit(row)
					found = true
					break
				}
			}
			if !found {
				return errors.New("missing")
			}
		}
		return nil
	case "container ls":
		for _, c := range x.containers {
			io.WriteString(w, c.ID+"\n")
		}
		return nil
	case "container inspect":
		for _, id := range args[4:] {
			for _, c := range x.containers {
				if c.ID == id {
					emit(c)
				}
			}
		}
		return nil
	case "network create":
		if x.entered != nil {
			close(x.entered)
			select {
			case <-x.gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		name := args[len(args)-1]
		for _, row := range x.rows {
			if row.Name == name {
				return errors.New("exists")
			}
		}
		id := strings.Repeat("4", 64)
		x.rows = append(x.rows, networkMetadata{ID: id, Name: name, Driver: "bridge", Scope: "local", Labels: map[string]string{"io.reefhq.created": strings.TrimPrefix(args[5], "io.reefhq.created=")}, IPAM: json.RawMessage(`{}`)})
		io.WriteString(w, id+"\n")
		return nil
	case "network rm":
		for i, row := range x.rows {
			if row.ID == args[2] {
				x.rows = append(x.rows[:i], x.rows[i+1:]...)
				return nil
			}
		}
		return errors.New("missing")
	case "network connect", "network disconnect":
		for i, c := range x.containers {
			if c.ID != args[3] {
				continue
			}
			if args[1] == "connect" {
				x.containers[i].Networks["app-net"] = args[2]
			} else {
				delete(x.containers[i].Networks, "app-net")
			}
			return nil
		}
		return errors.New("missing")
	}
	return errors.New("unexpected command")
}
func networkTargets(t *testing.T, s *ImageService) []NetworkTarget {
	t.Helper()
	rows, _, err := s.networks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	targets := []NetworkTarget{}
	for _, row := range rows {
		targets = append(targets, NetworkTarget{row.ID, row.Fingerprint})
	}
	return targets
}
func TestNetworksUsageProtectionAndSelectedMetadata(t *testing.T) {
	x := networkFixture()
	x.containers = []NetworkContainer{{ID: oldContainer, Name: "stopped-db", State: "exited", Networks: map[string]string{"app-net": networkA}}}
	s := &ImageService{Exec: x}
	rows, _, err := s.networks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]CatalogNetwork{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	if byID[networkA].Removable || len(byID[networkA].Containers) != 1 || byID[networkC].Mutable || !byID[networkB].Removable {
		t.Fatal(rows)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "private-") {
		t.Fatal("metadata leaked", string(raw))
	}
	for _, id := range []string{networkA, networkC} {
		if err := s.mutateNetwork(context.Background(), "remove", NetworkTarget{id, byID[id].Fingerprint}, ""); err == nil {
			t.Fatal("protected network removed", id)
		}
	}
	if err := s.mutateNetwork(context.Background(), "remove", NetworkTarget{networkB, byID[networkB].Fingerprint}, ""); err != nil {
		t.Fatal(err)
	}
}
func TestNetworksConnectionsRecheckIdentityNamespaceAndReefProtection(t *testing.T) {
	for _, mode := range []string{"normal", "stopped", "protected", "shared", "host", "changed", "missing"} {
		t.Run(mode, func(t *testing.T) {
			x := networkFixture()
			x.containers = []NetworkContainer{{ID: oldContainer, Name: "app", State: "running", Mode: "default", Networks: map[string]string{}}}
			s := &ImageService{Exec: x}
			target := networkTargets(t, s)[0]
			switch mode {
			case "stopped":
				x.containers[0].State = "exited"
			case "protected":
				x.containers[0].Protected = true
			case "shared":
				x.containers[0].Mode = "container:another"
			case "host":
				x.containers[0].Mode = "host"
			case "changed":
				x.rows[0].Options["password"] = "changed"
			case "missing":
				x.rows[0].ID = strings.Repeat("5", 64)
			}
			err := s.mutateNetwork(context.Background(), "connect", target, oldContainer)
			if mode != "normal" {
				if err == nil {
					t.Fatal("unsafe connection accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.mutateNetwork(context.Background(), "connect", target, oldContainer); err == nil {
				t.Fatal("duplicate connection accepted")
			}
			if err = s.mutateNetwork(context.Background(), "disconnect", target, oldContainer); err != nil {
				t.Fatal(err)
			}
			if len(x.containers[0].Networks) != 0 {
				t.Fatal(x.containers)
			}
		})
	}
	x := networkFixture()
	x.containers = []NetworkContainer{{ID: oldContainer, Name: "reef", Protected: true, Project: "reef", Networks: map[string]string{"app-net": networkA}}, {ID: newContainer, Name: "sidecar", Project: "reef", Networks: map[string]string{}}}
	s := &ImageService{Exec: x}
	rows, containers, err := s.networks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == networkA && row.Mutable {
			t.Fatal("Reef network mutable")
		}
	}
	for _, c := range containers {
		if !c.Protected {
			t.Fatal("Reef project sidecar unprotected")
		}
	}
}
func TestNetworksAdmissionResultsAndNotifications(t *testing.T) {
	x := networkFixture()
	x.gate = make(chan struct{})
	x.entered = make(chan struct{})
	m, p, rev := fixture(t, &fakeExecutor{})
	m.Images = &ImageService{Exec: x}
	var mu sync.Mutex
	events := []notifications.Event{}
	m.Notify = func(e notifications.Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) }
	if _, err := m.beginNetworks("create", "new-net", "", ""); err != nil {
		t.Fatal(err)
	}
	<-x.entered
	if _, err := m.beginVolumes("create", "data", ""); err == nil {
		t.Fatal("volume overlapped network operation")
	}
	if _, err := m.beginCatalog("pull", "app:latest", "", ""); err == nil {
		t.Fatal("image overlapped network operation")
	}
	if err := m.begin(p.ID, "stop", rev, ""); err == nil {
		t.Fatal("Compose overlapped network operation")
	}
	close(x.gate)
	m.wg.Wait()
	op := m.Images.networkOperation()
	if op.Running || len(op.Results) != 1 || !op.Results[0].OK {
		t.Fatal(op)
	}
	if len(events) != 2 {
		t.Fatal(events)
	}
	for _, e := range events {
		if !e.Valid() || e.Scope != "network" {
			t.Fatal(e)
		}
	}
	for _, raw := range []string{"[]", "{}", `[{"id":"--force","fingerprint":"bad"}]`} {
		if _, err := m.beginNetworks("remove", "", raw, ""); err == nil {
			t.Fatal(raw)
		}
	}
	for _, name := range []string{"--help", "../network", "a", strings.Repeat("a", 256)} {
		if _, err := m.beginNetworks("create", name, "", ""); err == nil {
			t.Fatal(name)
		}
	}
	targets := networkTargets(t, m.Images)
	raw, _ := json.Marshal([]NetworkTarget{targets[0], targets[0]})
	if _, err := m.beginNetworks("remove", "", string(raw), ""); err == nil {
		t.Fatal("duplicate selection accepted")
	}
	x.fail = true
	results := m.Images.runNetworks(context.Background(), "cleanup", "", targets, "")
	for _, r := range results {
		if r.OK || strings.Contains(r.Message, "secret") {
			t.Fatal(r)
		}
	}
	for _, args := range x.calls {
		for _, arg := range args {
			if arg == "prune" || arg == "--force" {
				t.Fatal(args)
			}
		}
	}
}
func TestNetworksAPISecurityAndRemoteIsolation(t *testing.T) {
	local, _, _ := fixture(t, &fakeExecutor{})
	lx := networkFixture()
	local.Images = &ImageService{Exec: lx}
	central := newServer(local, Config{AgentsEnabled: true}, "password-long-enough")
	central.agents, _ = agents.Open(t.TempDir())
	central.sessions[sessionKey("cookie")] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "cookie"}
	remote, _, _ := fixture(t, &fakeExecutor{})
	rx := networkFixture()
	rx.rows[0].Name = "remote-data"
	remote.Images = &ImageService{Exec: rx}
	backend := newServer(remote, Config{}, "unused-password")
	backend.discovery = &inventoryStub{}
	legacy, stop := relayAgentFixture(t, central, backend, "old-agent", agents.ImagesCapability)
	defer stop()
	h := central.Handler()
	if w := call(h, "GET", "/api/networks?host="+legacy, "", "", cookie); w.Code != 409 {
		t.Fatal(w.Code)
	}
	host, stopReady := relayAgentFixture(t, central, backend, "capable-agent", agents.NetworksCapability)
	defer stopReady()
	for _, tc := range []struct {
		method, path, body, origin string
		cookie                     *http.Cookie
		status                     int
	}{
		{"GET", "/api/networks", "", "", nil, 401}, {"GET", "/api/networks/action", "", "", cookie, 405},
		{"POST", "/api/networks/action", "csrf=csrf&confirmed=yes&action=create&name=new-data", "http://evil.test", cookie, 403},
		{"POST", "/api/networks/action", "csrf=wrong&confirmed=yes&action=create&name=new-data", "http://reef.test", cookie, 403},
		{"POST", "/api/networks/action", "csrf=csrf&action=create&name=new-data", "http://reef.test", cookie, 400},
	} {
		w := call(h, tc.method, tc.path, tc.body, tc.origin, tc.cookie)
		if w.Code != tc.status {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	w := call(h, "GET", "/api/networks?host="+host, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "remote-data") || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	body := url.Values{"csrf": {"csrf"}, "confirmed": {"yes"}, "action": {"create"}, "name": {"remote-created"}}.Encode()
	w = call(h, "POST", "/api/networks/action?host="+host, body, "http://reef.test", cookie)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	remote.wg.Wait()
	if len(lx.calls) > 0 {
		t.Fatal("remote request touched local Docker")
	}
	w = call(h, "GET", "/api/networks/operation?host="+host, "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Completed") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestNetworksUnknownEndpointsAndLargeSelections(t *testing.T) {
	x := networkFixture()
	x.rows[1].Endpoints = map[string]struct{ Name string }{strings.Repeat("f", 64): {Name: "external"}}
	s := &ImageService{Exec: x}
	rows, _, err := s.networks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == networkB && (row.Removable || len(row.Containers) != 1) {
			t.Fatal("unknown endpoint ignored", row)
		}
	}
	m, _, _ := fixture(t, &fakeExecutor{})
	m.Images = s
	targets := []NetworkTarget{}
	for i := 0; i < 101; i++ {
		targets = append(targets, NetworkTarget{ID: fmt.Sprintf("%064x", i+1000), Fingerprint: strings.Repeat("a", 64)})
	}
	raw, _ := json.Marshal(targets)
	if _, err := m.beginNetworks("cleanup", "", string(raw), ""); err != nil {
		t.Fatal("large selection rejected", err)
	}
	m.wg.Wait()
	op := s.networkOperation()
	if len(op.Results) != 101 || op.Running {
		t.Fatal(op)
	}
	for _, args := range x.calls {
		if len(args) > 1 && args[1] == "rm" {
			t.Fatal("nonexistent selection caused deletion", args)
		}
	}
}
