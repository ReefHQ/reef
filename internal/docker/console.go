package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type Console interface {
	io.ReadWriteCloser
	Resize(context.Context, int, int) error
}
type ConsoleOpener interface {
	OpenConsole(context.Context, string, string, int, int) (Console, error)
}

func ValidConsoleSize(cols, rows int) bool {
	return cols >= 2 && cols <= 300 && rows >= 2 && rows <= 150
}
func ValidShell(shell string) bool { return shell == "/bin/sh" || shell == "/bin/bash" }

func (c *Client) consolePost(ctx context.Context, path string, body any, result any) error {
	b, e := json.Marshal(body)
	if e != nil {
		return e
	}
	r, e := http.NewRequestWithContext(ctx, "POST", "http://docker"+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/json")
	response, e := c.http.Do(r)
	if e != nil {
		return fmt.Errorf("Cannot reach Docker for the console.")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Docker console request failed (HTTP %d). Check that the container is running and the selected shell exists.", response.StatusCode)
	}
	if result != nil {
		if e := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(result); e != nil {
			return fmt.Errorf("Docker returned an invalid console response.")
		}
	}
	return nil
}

// OpenConsole creates a non-privileged exec, inheriting the container's user.
// No host shell, user override, environment inspection or container attach is used.
func (c *Client) OpenConsole(ctx context.Context, id, shell string, cols, rows int) (Console, error) {
	if !ValidContainerID(id) || !ValidShell(shell) || !ValidConsoleSize(cols, rows) {
		return nil, fmt.Errorf("Invalid console options.")
	}
	var created struct {
		ID string `json:"Id"`
	}
	if e := c.consolePost(ctx, "/containers/"+id+"/exec", map[string]any{
		"AttachStdin": true, "AttachStdout": true, "AttachStderr": true, "Tty": true,
		"Privileged": false, "Cmd": []string{shell}, "Env": []string{"TERM=xterm-256color"},
		"ConsoleSize": []int{rows, cols},
	}, &created); e != nil {
		return nil, e
	}
	if !ValidContainerID(created.ID) {
		return nil, fmt.Errorf("Docker returned an invalid exec ID.")
	}
	conn, e := c.dial(ctx)
	if e != nil {
		return nil, fmt.Errorf("Cannot open the Docker console connection.")
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	failed := true
	defer func() {
		if failed {
			_ = conn.Close()
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
	b, _ := json.Marshal(map[string]any{"Detach": false, "Tty": true, "ConsoleSize": []int{rows, cols}})
	r, _ := http.NewRequest("POST", "http://docker/exec/"+created.ID+"/start", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "tcp")
	if e = r.Write(conn); e != nil {
		return nil, fmt.Errorf("Cannot start the Docker console.")
	}
	reader := bufio.NewReader(conn)
	response, e := http.ReadResponse(reader, r)
	if e != nil {
		return nil, fmt.Errorf("Cannot start the Docker console.")
	}
	if response.StatusCode != 101 && response.StatusCode != 200 {
		return nil, fmt.Errorf("Docker could not start this shell (HTTP %d). Check that the selected shell exists.", response.StatusCode)
	}
	var output io.Reader = reader
	if response.StatusCode == 200 {
		output = response.Body
	}
	_ = conn.SetDeadline(time.Time{})
	failed = false
	return &socketConsole{conn: conn, reader: output, client: c, id: created.ID}, nil
}

type socketConsole struct {
	conn    net.Conn
	reader  io.Reader
	client  *Client
	id      string
	writeMu sync.Mutex
}

func (s *socketConsole) Read(b []byte) (int, error) { return s.reader.Read(b) }
func (s *socketConsole) Write(b []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return s.conn.Write(b)
}
func (s *socketConsole) Close() error { return s.conn.Close() }
func (s *socketConsole) Resize(ctx context.Context, cols, rows int) error {
	if !ValidConsoleSize(cols, rows) {
		return fmt.Errorf("Invalid terminal size.")
	}
	return s.client.consolePost(ctx, "/exec/"+s.id+"/resize?w="+strconv.Itoa(cols)+"&h="+strconv.Itoa(rows), nil, nil)
}
