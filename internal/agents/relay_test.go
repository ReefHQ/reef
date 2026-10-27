package agents

import (
	"context"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"
)

func relayCommand() Command {
	return Command{Owner: strings.Repeat("a", 64), Expires: time.Now().Add(time.Hour), Method: "GET", Path: "/api/state"}
}
func TestRelayRoundtripIsolationAndGeneration(t *testing.T) {
	r := NewRelay()
	r.Connect("one", "generation")
	r.Connect("two", "generation-two")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan string, 1)
	go func() {
		response, err := r.Do(ctx, "one", relayCommand())
		if err != nil {
			result <- err.Error()
			return
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			result <- err.Error()
			return
		}
		result <- string(data)
	}()
	batch, err := r.Poll(ctx, "one", "generation")
	if err != nil || len(batch.Commands) != 1 {
		t.Fatal(batch, err)
	}
	id := batch.Commands[0].ID
	if err = r.Results("two", "generation-two", []Frame{{ID: id, Sequence: 1, Status: 200, Done: true, Data: []byte("wrong host")}}); err != nil {
		t.Fatal(err)
	}
	if err = r.Results("one", "wrong-generation", []Frame{{ID: id, Sequence: 1, Status: 200}}); err == nil {
		t.Fatal("wrong generation accepted")
	}
	if err = r.Results("one", "generation", []Frame{{ID: id, Sequence: 1, Status: 200, ContentType: "application/json"}, {ID: id, Sequence: 2, Data: []byte(`{"host":"one"}`)}, {ID: id, Sequence: 3, Done: true}}); err != nil {
		t.Fatal(err)
	}
	if got := <-result; got != `{"host":"one"}` {
		t.Fatal(got)
	}
}
func TestRelayCancellationNeverRedeliversCommands(t *testing.T) {
	r := NewRelay()
	r.Connect("host", "gen")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _, _ = r.Do(ctx, "host", relayCommand()) }()
	batch, err := r.Poll(context.Background(), "host", "gen")
	if err != nil || len(batch.Commands) != 1 {
		t.Fatal(err)
	}
	cancel()
	<-done
	batch, err = r.Poll(context.Background(), "host", "gen")
	if err != nil || len(batch.Commands) != 0 || len(batch.Cancel) != 1 {
		t.Fatal("command replayed or cancellation missing", batch, err)
	}
}
func TestRelayReconnectClosesActiveStreams(t *testing.T) {
	r := NewRelay()
	r.Connect("host", "old")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		response, err := r.Do(ctx, "host", relayCommand())
		if err != nil {
			done <- err
			return
		}
		defer response.Body.Close()
		_, err = io.ReadAll(response.Body)
		done <- err
	}()
	batch, _ := r.Poll(ctx, "host", "old")
	if len(batch.Commands) != 1 {
		t.Fatal(batch)
	}
	r.Results("host", "old", []Frame{{ID: batch.Commands[0].ID, Sequence: 1, Status: 200}})
	r.Connect("host", "new")
	if err := <-done; err == nil {
		t.Fatal("stream survived replacement")
	}
	if _, err := r.Poll(ctx, "host", "old"); err == nil {
		t.Fatal("old generation polled")
	}
}
func TestRelayAllowlistAndResponseLimits(t *testing.T) {
	for _, path := range []string{"/login", "/logout", "/api/hosts", "/api/console/stream", "/agent/v1/enroll", "/api/../api/state", "http://evil.test"} {
		if AllowedCommand("POST", path) || AllowedCommand("GET", path) {
			t.Fatal("unsafe command", path)
		}
	}
	r := NewRelay()
	r.Connect("host", "gen")
	command := relayCommand()
	command.Form = url.Values{"content": {strings.Repeat("x", MaxCommandBytes+1)}}
	if _, err := r.Do(context.Background(), "host", command); err == nil {
		t.Fatal("oversized command accepted")
	}
}

func TestRelaySanitizesAgentHeadersAndReleasesBudget(t *testing.T) {
	relay := NewRelay()
	relay.Connect("host", "gen")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan string, 1)
	go func() {
		response, err := relay.Do(ctx, "host", relayCommand())
		if err != nil {
			result <- err.Error()
			return
		}
		kind := response.Header.Get("Content-Type")
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		result <- kind
	}()
	batch, _ := relay.Poll(ctx, "host", "gen")
	relay.Results("host", "gen", []Frame{{ID: batch.Commands[0].ID, Sequence: 1, Status: 200, ContentType: "text/html", Data: []byte("<script>evil</script>"), Done: true}})
	if got := <-result; got != "text/plain; charset=utf-8" {
		t.Fatal(got)
	}
	relay.mu.Lock()
	defer relay.mu.Unlock()
	if relay.total != 0 || relay.bytes != 0 {
		t.Fatal("relay budget leaked", relay.total, relay.bytes)
	}
}
