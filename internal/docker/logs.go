package docker

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
)

const MaxLogBytes = 256 << 10
const maxLogWireBytes = 1 << 20

var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidContainerID(id string) bool { return containerID.MatchString(id) }

type Logs struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

type LogReader interface {
	ReadLogs(context.Context, string, string, int) (Logs, error)
}

// ReadLogs is a bounded snapshot, never attach/exec or an unbounded follow stream.
func (c *Client) ReadLogs(ctx context.Context, id, stream string, tail int) (Logs, error) {
	if !ValidContainerID(id) || (stream != "all" && stream != "stdout" && stream != "stderr") || tail < 1 || tail > 1000 {
		return Logs{}, fmt.Errorf("Invalid log options.")
	}
	q := url.Values{"follow": {"0"}, "timestamps": {"1"}, "tail": {strconv.Itoa(tail)}}
	if stream != "stderr" {
		q.Set("stdout", "1")
	}
	if stream != "stdout" {
		q.Set("stderr", "1")
	}
	r, e := http.NewRequestWithContext(ctx, "GET", "http://docker/containers/"+id+"/logs?"+q.Encode(), nil)
	if e != nil {
		return Logs{}, e
	}
	response, e := c.http.Do(r)
	if e != nil {
		return Logs{}, fmt.Errorf("Cannot reach Docker to read logs.")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == 404 {
			return Logs{}, fmt.Errorf("This container no longer exists. Refresh the container list.")
		}
		return Logs{}, fmt.Errorf("Docker logs are unavailable (HTTP %d). The logging driver may not support reading logs.", response.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(response.Body, maxLogWireBytes+1))
	if e != nil {
		return Logs{}, fmt.Errorf("Cannot read Docker logs. Try refreshing.")
	}
	cut := len(b) > maxLogWireBytes
	if cut {
		b = b[:maxLogWireBytes]
	}
	return decodeLogs(b, cut)
}

func decodeLogs(b []byte, cut bool) (Logs, error) {
	// Docker's older API uses raw-stream for both formats. With timestamps enabled,
	// TTY text starts with a timestamp; non-TTY output starts with a binary header.
	framed := len(b) >= 4 && b[0] <= 3 && b[1] == 0 && b[2] == 0 && b[3] == 0
	out := make([]byte, 0)
	if !framed {
		out = b
	} else {
		for len(b) > 0 {
			if len(b) < 8 {
				if cut {
					break
				}
				return Logs{}, fmt.Errorf("Docker returned an incomplete log stream.")
			}
			if b[0] > 3 || b[1] != 0 || b[2] != 0 || b[3] != 0 {
				return Logs{}, fmt.Errorf("Docker returned an invalid log stream.")
			}
			// Stream 3 carries daemon errors rather than application output.
			if b[0] == 3 {
				return Logs{}, fmt.Errorf("Docker could not read the container logs.")
			}
			n := uint64(binary.BigEndian.Uint32(b[4:8]))
			b = b[8:]
			if n > uint64(len(b)) {
				if !cut {
					return Logs{}, fmt.Errorf("Docker returned an incomplete log stream.")
				}
				n = uint64(len(b))
			}
			remaining := MaxLogBytes - len(out)
			if n > uint64(remaining) {
				out = append(out, b[:remaining]...)
				cut = true
				break
			}
			out = append(out, b[:int(n)]...)
			b = b[int(n):]
		}
	}
	if len(out) > MaxLogBytes {
		out = out[:MaxLogBytes]
		cut = true
	}
	return Logs{Text: string(out), Truncated: cut}, nil
}
