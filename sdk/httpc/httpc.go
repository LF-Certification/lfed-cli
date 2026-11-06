package httpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"go.ed.linuxfoundation.org/lfed/sdk/config"
)

type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

func (e *HTTPError) Is(target error) bool {
	var t *HTTPError
	return errors.As(target, &t) && t.StatusCode == e.StatusCode
}

var (
	ErrUnauthorized = &HTTPError{StatusCode: http.StatusUnauthorized}
	ErrForbidden    = &HTTPError{StatusCode: http.StatusForbidden}
	ErrNotFound     = &HTTPError{StatusCode: http.StatusNotFound}
	ErrConflict     = &HTTPError{StatusCode: http.StatusConflict}
)

type Client struct {
	baseURL   string
	token     string
	http      *http.Client
	userAgent string
}

func debugf(format string, args ...any) {
	if config.Debug() {
		fmt.Fprintf(os.Stderr, "lfed-plugin: debug: "+format+"\n", args...)
	}
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

func WithUserAgent(name string) Option {
	return func(c *Client) { c.userAgent = "lfed-plugin/" + name }
}

func New(baseURL, token string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(baseURL, "/")
	if err := checkBaseURL(base); err != nil {
		return nil, err
	}
	c := &Client{
		baseURL: base,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		debugf("%s %s: %v", method, req.URL, err)
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	debugf("%s %s -> %d (%d bytes, %s)", method, req.URL, resp.StatusCode, len(raw), time.Since(started).Round(time.Millisecond))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &HTTPError{StatusCode: resp.StatusCode, Message: message(resp.StatusCode, raw)}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func message(status int, raw []byte) string {
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Detail) > 0 {
		var s string
		if json.Unmarshal(envelope.Detail, &s) == nil {
			return s
		}
		return string(envelope.Detail)
	}
	if msg := strings.TrimSpace(string(raw)); msg != "" {
		return msg
	}
	return http.StatusText(status)
}

func checkBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err == nil && u.Host != "" {
		switch u.Scheme {
		case "https":
			return nil
		case "http":
			if isLoopback(u.Hostname()) {
				return nil
			}
		}
	}
	return fmt.Errorf("API base URL %q must use https (plain http is allowed for loopback only)", raw)
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
