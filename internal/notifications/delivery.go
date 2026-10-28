package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var discordPath = regexp.MustCompile(`^/api/(v[0-9]+/)?webhooks/[0-9]+/[A-Za-z0-9._-]+/?$`)

func validate(c Config) (*url.URL, error) {
	u, err := url.Parse(c.URL)
	if err != nil || len(c.URL) > 2048 || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || strings.ContainsAny(c.URL, "\r\n\x00") {
		return nil, errors.New("Enter a valid HTTP or HTTPS webhook URL without embedded credentials or fragments")
	}
	if c.Format != "discord" && c.Format != "json" {
		return nil, errors.New("Choose Discord or JSON format")
	}
	if c.Format == "discord" && (!(u.Hostname() == "discord.com" || u.Hostname() == "discordapp.com" || u.Hostname() == "canary.discord.com" || u.Hostname() == "ptb.discord.com") || u.Scheme != "https" || (u.Port() != "" && u.Port() != "443") || !discordPath.MatchString(u.Path)) {
		return nil, errors.New("Enter an official Discord incoming webhook HTTPS URL")
	}
	return u, nil
}
func payload(c Config, e Event) ([]byte, string, error) {
	u, err := validate(c)
	if err != nil {
		return nil, "", err
	}
	var body any = struct {
		Schema string `json:"schema"`
		Event  Event  `json:"event"`
	}{"reef.action.v1", e}
	if c.Format == "discord" {
		q := u.Query()
		q.Set("wait", "true")
		u.RawQuery = q.Encode()
		body = map[string]any{"username": "Reef", "content": e.Message(), "allowed_mentions": map[string]any{"parse": []string{}}, "flags": 4}
	}
	b, err := json.Marshal(body)
	return b, u.String(), err
}
func seconds(s string) time.Duration {
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
		return 0
	}
	if n > 7*24*60*60 {
		return 7 * 24 * time.Hour
	}
	return time.Duration(n * float64(time.Second))
}
func retryDelay(header string, body []byte) time.Duration {
	d := seconds(header)
	if d == 0 {
		if date, err := http.ParseTime(header); err == nil && time.Until(date) > 0 {
			d = time.Until(date)
		}
	}
	var v struct {
		RetryAfter float64 `json:"retry_after"`
	}
	if json.Unmarshal(body, &v) == nil {
		if b := seconds(strconv.FormatFloat(v.RetryAfter, 'f', -1, 64)); b > d {
			d = b
		}
	}
	return d
}

// SendOne handles one item. It never holds the queue mutex during network I/O.
func (s *Service) SendOne(ctx context.Context, client *http.Client) bool {
	s.mu.Lock()
	if s.inflight != nil || s.relay || !s.data.Config.Enabled || len(s.data.Pending) == 0 {
		s.mu.Unlock()
		return false
	}
	item := s.data.Pending[0]
	if time.Since(item.Event.Time) > 24*time.Hour {
		s.data.Pending = s.data.Pending[1:]
		s.data.Dropped++
		s.data.LastError = "Notification expired after 24 hours"
		_ = s.saveLocked()
		s.mu.Unlock()
		return true
	}
	if time.Now().Before(s.data.After) {
		s.mu.Unlock()
		return false
	}
	if time.Now().Before(item.Next) {
		s.mu.Unlock()
		return false
	}
	c, revision := s.data.Config, s.revision
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	s.inflight = cancel
	s.mu.Unlock()
	defer cancel()
	body, target, err := payload(c, item.Event)
	status := 0
	message := "Webhook delivery failed or timed out"
	delay := time.Duration(0)
	retry := true
	if err == nil {
		var request *http.Request
		request, err = http.NewRequestWithContext(requestCtx, "POST", target, bytes.NewReader(body))
		if err == nil {
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("User-Agent", "Reef")
			request.Header.Set("X-Reef-Event-ID", item.Event.Key())
			// Copy even injected clients so redirect handling cannot expose the URL token.
			copyClient := *client
			copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			var response *http.Response
			response, err = copyClient.Do(request)
			if err == nil {
				status = response.StatusCode
				responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
				response.Body.Close()
				delay = retryDelay(response.Header.Get("Retry-After"), responseBody)
				if status >= 200 && status < 300 && response.Header.Get("X-RateLimit-Remaining") == "0" {
					if reset := seconds(response.Header.Get("X-RateLimit-Reset-After")); reset > delay {
						delay = reset
					}
				}
				message = fmt.Sprintf("Webhook returned HTTP %d", status)
				retry = status == 429 || status >= 500
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight = nil
	if revision != s.revision || len(s.data.Pending) == 0 || s.data.Pending[0].Event.Key() != item.Event.Key() {
		return true
	}
	if delay > 0 {
		s.data.After = time.Now().Add(delay)
	}
	if err == nil && status >= 200 && status < 300 {
		s.data.Pending = s.data.Pending[1:]
		s.data.Sent++
		s.data.LastSuccess = time.Now()
		s.data.LastError = ""
		if delay > 0 {
			s.data.After = time.Now().Add(delay)
		}
	} else {
		item.Attempts++
		s.data.LastError = message
		if !retry || item.Attempts >= 5 {
			s.data.Pending = s.data.Pending[1:]
			s.data.Dropped++
		} else {
			if delay <= 0 {
				delay = time.Second * time.Duration(1<<item.Attempts)
			}
			item.Next = time.Now().Add(delay)
			s.data.Pending[0] = item
			s.data.After = item.Next
		}
	}
	_ = s.saveLocked()
	return true
}
func (s *Service) Run(ctx context.Context) {
	client := &http.Client{Timeout: 5 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
			s.SendOne(ctx, client)
		}
	}
}
