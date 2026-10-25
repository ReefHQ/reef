package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConsoleExecProtocolAndResize(t *testing.T) {
	id, execID := strings.Repeat("a", 64), strings.Repeat("b", 64)
	client, server := net.Pipe()
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		r, e := http.ReadRequest(bufio.NewReader(server))
		if e != nil {
			done <- e
			return
		}
		b, e := io.ReadAll(r.Body)
		if e != nil {
			done <- e
			return
		}
		if r.URL.Path != "/exec/"+execID+"/start" || r.Header.Get("Upgrade") != "tcp" || !strings.Contains(string(b), `"Tty":true`) {
			done <- io.ErrUnexpectedEOF
			return
		}
		_, e = io.WriteString(server, "HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\nready> ")
		if e != nil {
			done <- e
			return
		}
		input := make([]byte, 5)
		_, e = io.ReadFull(server, input)
		if e != nil || string(input) != "echo\n" {
			done <- io.ErrUnexpectedEOF
			return
		}
		_, e = io.WriteString(server, "done\n")
		done <- e
	}()
	resize := false
	c := &Client{dial: func(context.Context) (net.Conn, error) { return client, nil }, http: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" {
			t.Fatal(r.Method)
		}
		if strings.HasSuffix(r.URL.Path, "/exec") {
			var body map[string]any
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Fatal(e)
			}
			if r.URL.Path != "/containers/"+id+"/exec" || body["Privileged"] != false || body["Tty"] != true || body["AttachStdin"] != true || body["User"] != nil || body["Cmd"].([]any)[0] != "/bin/sh" {
				t.Fatal(body)
			}
			return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(`{"Id":"` + execID + `"}`))}, nil
		}
		if r.URL.Path != "/exec/"+execID+"/resize" || r.URL.Query().Get("w") != "100" || r.URL.Query().Get("h") != "40" {
			t.Fatal(r.URL)
		}
		resize = true
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	console, e := c.OpenConsole(ctx, id, "/bin/sh", 80, 24)
	if e != nil {
		t.Fatal(e)
	}
	defer console.Close()
	buf := make([]byte, 7)
	if _, e = io.ReadFull(console, buf); e != nil || string(buf) != "ready> " {
		t.Fatal(string(buf), e)
	}
	if _, e = console.Write([]byte("echo\n")); e != nil {
		t.Fatal(e)
	}
	buf = make([]byte, 5)
	if _, e = io.ReadFull(console, buf); e != nil || string(buf) != "done\n" {
		t.Fatal(string(buf), e)
	}
	if e = console.Resize(ctx, 100, 40); e != nil || !resize {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}

func TestConsoleRejectsUnsafeOptionsBeforeDocker(t *testing.T) {
	c := &Client{}
	for _, tc := range []struct {
		id, shell  string
		cols, rows int
	}{{"bad", "/bin/sh", 80, 24}, {strings.Repeat("a", 64), "sh -c rm", 80, 24}, {strings.Repeat("a", 64), "/bin/sh", 301, 24}} {
		if _, e := c.OpenConsole(context.Background(), tc.id, tc.shell, tc.cols, tc.rows); e == nil {
			t.Fatal("unsafe console options accepted")
		}
	}
}
