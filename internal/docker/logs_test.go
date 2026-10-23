package docker

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func logFrame(stream byte, text string) string {
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(text)))
	return string(header) + text
}

func TestReadLogsFramingTTYAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, wire, text string
		cut, fail        bool
	}{
		{"multiplexed", logFrame(1, "timestamp hello\n") + logFrame(2, "timestamp error\n"), "timestamp hello\ntimestamp error\n", false, false},
		{"TTY", "2026-10-23T10:00:00Z terminal output\r\n", "2026-10-23T10:00:00Z terminal output\r\n", false, false},
		{"empty", "", "", false, false},
		{"oversized raw", strings.Repeat("x", MaxLogBytes+1), strings.Repeat("x", MaxLogBytes), true, false},
		{"oversized frame", logFrame(1, strings.Repeat("y", MaxLogBytes+1)), strings.Repeat("y", MaxLogBytes), true, false},
		{"oversized wire", logFrame(1, strings.Repeat("z", maxLogWireBytes+1)), strings.Repeat("z", MaxLogBytes), true, false},
		{"broken frame", logFrame(1, "hello")[:10], "", false, true},
		{"daemon error", logFrame(3, "private daemon detail"), "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{http: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.URL.Path != "/containers/"+strings.Repeat("a", 64)+"/logs" {
					t.Fatal(r.Method, r.URL)
				}
				q := r.URL.Query()
				if q.Get("follow") != "0" || q.Get("timestamps") != "1" || q.Get("tail") != "200" || q.Get("stdout") != "1" || q.Get("stderr") != "1" {
					t.Fatal(q)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.wire))}, nil
			})}}
			got, e := c.ReadLogs(context.Background(), strings.Repeat("a", 64), "all", 200)
			if (e != nil) != tc.fail {
				t.Fatal(e)
			}
			if !tc.fail && (got.Text != tc.text || got.Truncated != tc.cut) {
				t.Fatalf("len=%d truncated=%t", len(got.Text), got.Truncated)
			}
			if e != nil && strings.Contains(e.Error(), "private") {
				t.Fatal("leaked daemon error")
			}
		})
	}
}

func TestLogOptionsAndErrors(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		c := &Client{http: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			q := r.URL.Query()
			other := "stdout"
			if stream == "stdout" {
				other = "stderr"
			}
			if q.Get(stream) != "1" || q.Get(other) != "" {
				t.Fatal(q)
			}
			return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("private-driver-error"))}, nil
		})}}
		_, e := c.ReadLogs(context.Background(), strings.Repeat("b", 64), stream, 1000)
		if e == nil || strings.Contains(e.Error(), "private") {
			t.Fatal(e)
		}
	}
	called := false
	c := &Client{http: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) { called = true; return nil, errors.New("private-socket") })}}
	for _, tc := range []struct {
		id, stream string
		tail       int
	}{{"../../other", "all", 200}, {strings.Repeat("a", 64), "exec", 200}, {strings.Repeat("a", 64), "all", 1001}} {
		if _, e := c.ReadLogs(context.Background(), tc.id, tc.stream, tc.tail); e == nil || called {
			t.Fatal("unsafe request reached Docker")
		}
	}
	if _, e := c.ReadLogs(context.Background(), strings.Repeat("a", 64), "all", 1); e == nil || strings.Contains(e.Error(), "private") {
		t.Fatal(e)
	}
}
