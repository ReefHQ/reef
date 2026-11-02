package app

import (
	"context"
	"net/http"
	"net/url"
	"reef/internal/agents"
	"reflect"
	"strings"
	"testing"
)

func composeRestartFixture(t *testing.T) (*Server, *containerEngineFixture) {
	s, f := standaloneFixture(t)
	old := f.items[standaloneOldID]
	put(old.Config, "Labels", map[string]string{"com.docker.compose.project": "app", "com.docker.compose.service": "web"})
	old.State.Running = true
	old.State.Status = "running"
	f.items[old.ID] = old
	return s, f
}
func TestComposeContainerRestartTouchesOnlyTheExactContainer(t *testing.T) {
	s, f := composeRestartFixture(t)
	old := f.items[standaloneOldID]
	before := inspectFingerprint(old)
	sibling := old
	sibling.ID = standaloneNewID
	sibling.Name = "/sibling"
	f.items[sibling.ID] = sibling
	siblingFingerprint := inspectFingerprint(sibling)
	op := runStandalone(t, s, "restart")
	if !op.OK || op.Unknown {
		t.Fatal(op)
	}
	mutations := []string{}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "POST ") || strings.HasPrefix(call, "DELETE ") {
			mutations = append(mutations, call)
		}
	}
	if !reflect.DeepEqual(mutations, []string{"POST /containers/" + standaloneOldID + "/restart?t=20"}) {
		t.Fatal(mutations)
	}
	if inspectFingerprint(f.items[standaloneOldID]) != before || f.pulls != 0 || len(f.items) != 2 || inspectFingerprint(f.items[standaloneNewID]) != siblingFingerprint {
		t.Fatal("Restart changed identity, configuration or images")
	}
}
func TestComposeIndividualRestartKeepsOtherActionsAndProtectedContainersBlocked(t *testing.T) {
	for _, action := range []string{"start", "stop", "update", "remove"} {
		s, f := composeRestartFixture(t)
		op := runStandalone(t, s, action)
		if op.OK || op.Unknown {
			t.Fatal(action, op)
		}
		for _, c := range f.calls {
			if strings.HasPrefix(c, "POST ") || strings.HasPrefix(c, "DELETE ") {
				t.Fatal(c)
			}
		}
	}
	for _, mode := range []string{"reef", "swarm", "socket", "paused", "exited", "namespace"} {
		s, f := composeRestartFixture(t)
		old := f.items[standaloneOldID]
		switch mode {
		case "reef":
			put(old.Config, "Labels", map[string]string{"com.docker.compose.project": "app", "io.reef.instance": "true"})
		case "swarm":
			put(old.Config, "Labels", map[string]string{"com.docker.compose.project": "app", "com.docker.swarm.service.id": "swarm"})
		case "socket":
			old.Mounts[0].Destination = "/var/run/docker.sock"
		case "paused":
			old.State.Paused = true
		case "exited":
			old.State.Running = false
			old.State.Status = "exited"
		case "namespace":
			put(old.HostConfig, "NetworkMode", "container:"+standaloneNewID)
		}
		f.items[old.ID] = old
		op := runStandalone(t, s, "restart")
		if op.OK {
			t.Fatal(mode, op)
		}
		for _, c := range f.calls {
			if strings.HasPrefix(c, "POST ") {
				t.Fatal(mode, c)
			}
		}
	}
	s, _ := composeRestartFixture(t)
	s.m.Projects["test"].ImageUpdates.Checking = true
	if _, e := s.m.beginContainer("restart", standaloneOldID, strings.Repeat("f", 64)); e == nil {
		t.Fatal("Concurrent image check allowed")
	}
}
func TestComposeContainerRestartUsesOnlyCapableSelectedAgent(t *testing.T) {
	central, _, cookie, csrf := userServer(t)
	backend, f := composeRestartFixture(t)
	r, _, e := backend.m.containers.review(context.Background(), standaloneOldID)
	if e != nil || r.Allowed || !r.RestartAllowed {
		t.Fatal(r, e)
	}
	host, stop := relayAgentFixture(t, central, backend, "compose-old", agents.ContainersCapability)
	defer stop()
	form := url.Values{"csrf": {csrf}, "confirmed": {"yes"}, "action": {"restart"}, "compose_restart": {"yes"}, "id": {standaloneOldID}, "fingerprint": {r.Fingerprint}}
	w := call(central.Handler(), "POST", "/api/container-management/action?host="+host, form.Encode(), "http://reef.test", cookie)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	host, stop = relayAgentFixture(t, central, backend, "compose-new", agents.ContainersCapability, agents.ComposeContainerRestartCapability)
	defer stop()
	w = call(central.Handler(), "POST", "/api/container-management/action?host="+host, form.Encode(), "http://reef.test", cookie)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	backend.m.wg.Wait()
	op, e := backend.m.containers.snapshot()
	if e != nil || !op.OK {
		t.Fatal(op, e)
	}
	calls := 0
	for _, v := range f.calls {
		if strings.HasPrefix(v, "POST ") {
			calls++
		}
	}
	if calls != 1 {
		t.Fatal(f.calls)
	}
}

func TestComposeContainerRestartRechecksFingerprintImmediatelyBeforeMutation(t *testing.T) {
	s, f := composeRestartFixture(t)
	reads := 0
	s.m.containers.api.http.Transport = containerRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" && r.URL.Path == "/containers/"+standaloneOldID+"/json" {
			reads++
			if reads == 3 {
				f.mu.Lock()
				v := f.items[standaloneOldID]
				v.Name = "/externally-changed"
				f.items[v.ID] = v
				f.mu.Unlock()
			}
		}
		return f.roundTrip(r)
	})
	op := runStandalone(t, s, "restart")
	if op.OK || op.Unknown {
		t.Fatal(op)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "POST ") {
			t.Fatal("Changed target was restarted", c)
		}
	}
	s.m.storageBusy = true
	if e := s.m.checkImages("test", false); e == nil || !strings.Contains(e.Error(), "resource operation") {
		t.Fatal("Image check admitted during restart", e)
	}
}
