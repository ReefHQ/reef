package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"reef/internal/docker"
)

type logStub struct {
	inventoryStub
	reads    int
	stream   string
	tail     int
	logError error
}

func (f *logStub) ReadLogs(ctx context.Context, id, stream string, tail int) (docker.Logs, error) {
	f.reads++
	f.stream = stream
	f.tail = tail
	return docker.Logs{Text: "<script>application output</script>\n"}, f.logError
}
func TestContainerLogsAuthValidationAndReadOnlyDiscovery(t *testing.T) {
	executor := &fakeExecutor{}
	m, _, _ := fixture(t, executor)
	s := newServer(m, Config{}, "long-password-123456", false)
	id := strings.Repeat("a", 64)
	reader := &logStub{inventoryStub: inventoryStub{rows: []docker.Container{{ID: id, Name: "unmanaged app", State: "exited", ComposeProject: "unmanaged"}}}}
	s.discovery = reader
	h := s.Handler()
	path := "/api/container-logs?id=" + id
	if w := call(h, "GET", path, "", "", nil); w.Code != 401 || reader.calls != 0 || reader.reads != 0 {
		t.Fatal(w.Code)
	}
	s.sessions["logs-test"] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "qcu_session", Value: "logs-test"}
	for _, query := range []string{"id=../../etc/passwd", "id=" + id + "&tail=1001", "id=" + id + "&tail=bad", "id=" + id + "&stream=exec"} {
		if w := call(h, "GET", "/api/container-logs?"+query, "", "", cookie); w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	if reader.calls != 0 || reader.reads != 0 {
		t.Fatal("invalid log query reached Docker")
	}
	if w := call(h, "GET", "/api/container-logs?id="+strings.Repeat("b", 64), "", "", cookie); w.Code != 404 || reader.reads != 0 {
		t.Fatal(w.Code)
	}
	w := call(h, "GET", path, "", "", cookie)
	if w.Code != 200 || reader.reads != 1 || reader.tail != 200 || reader.stream != "all" {
		t.Fatal(w.Code, reader)
	}
	if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "application output") {
		t.Fatal(w.Header(), w.Body.String())
	}
	w = call(h, "GET", path+"&stream=stderr&tail=500", "", "", cookie)
	if w.Code != 200 || reader.stream != "stderr" || reader.tail != 500 {
		t.Fatal(w.Code)
	}
	w = call(h, "POST", path, "csrf=csrf", "http://reef.test", cookie)
	if w.Code != 405 || reader.reads != 2 {
		t.Fatal(w.Code, reader.reads)
	}
	if len(executor.calls) != 0 || len(m.Projects) != 1 {
		t.Fatal("log viewing changed project management")
	}
	reader.logError = errors.New("Log driver unavailable.")
	if w := call(h, "GET", path, "", "", cookie); w.Code != 503 {
		t.Fatal(w.Code)
	}
	reader.failure = errors.New("private inventory failure")
	if w := call(h, "GET", path, "", "", cookie); w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code)
	}
	s.demo = true
	if w := call(h, "GET", path, "", "", cookie); w.Code != 503 {
		t.Fatal(w.Code)
	}
}
