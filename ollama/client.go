// Package ollama is a dependency-free client for the HTTP APIs exposed by an
// Ollama server.
//
// It covers two endpoints:
//
//   - POST /v1/systemone, a decision endpoint that evaluates a piece of text
//     (the "state") against a set of typed questions and returns probabilities
//     rather than prose.
//   - POST /v1/chat/completions, the OpenAI-compatible chat API, which also
//     works against remote providers that speak the same dialect.
//
// The decision endpoint is specific to Ollama builds that advertise the
// "decision" model capability. Point the client at the server with
// WithBaseURL; the default is http://localhost:11434.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is the address of a locally running Ollama server.
const DefaultBaseURL = "http://localhost:11434"

// maxResponseBytes caps how much of a response body is read, so a runaway
// server cannot exhaust memory.
const maxResponseBytes = 64 << 20 // 64 MiB

// Client talks to an Ollama server. It is safe for concurrent use as long as
// the HTTP client it was built with is.
type Client struct {
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
	headers    map[string]string
}

// Option customises a Client at construction time.
type Option func(*Client)

// WithBaseURL points the client at another server. Any trailing slash is
// trimmed. An empty or blank value is ignored.
func WithBaseURL(raw string) Option {
	return func(c *Client) {
		if trimmed := strings.TrimRight(strings.TrimSpace(raw), "/"); trimmed != "" {
			c.baseURL = trimmed
		}
	}
}

// WithHTTPClient supplies the underlying HTTP client, for callers that need
// custom transports, proxies or TLS settings. The timeout is applied on top of
// it, so the order of options does not matter.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// WithTimeout bounds each request. Leave it unset (the default) for streaming
// calls, whose bodies legitimately stay open for a long time; use a context
// deadline for those instead.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.timeout = d
	}
}

// WithHeader adds a header sent with every request.
func WithHeader(key, value string) Option {
	return func(c *Client) {
		if key = strings.TrimSpace(key); key != "" {
			c.headers[key] = value
		}
	}
}

// WithAPIKey sends an "Authorization: Bearer <key>" header, which is how
// remote OpenAI-compatible gateways authenticate.
func WithAPIKey(key string) Option {
	return WithHeader("Authorization", "Bearer "+strings.TrimSpace(key))
}

// New builds a Client. The zero configuration targets a local Ollama server
// with no overall request timeout.
func New(opts ...Option) *Client {
	c := &Client{
		baseURL: DefaultBaseURL,
		headers: make(map[string]string),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{}
	}
	if c.timeout > 0 {
		c.httpClient.Timeout = c.timeout
	}
	return c
}

// BaseURL returns the server address in use.
func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("ollama: encoding request body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("ollama: building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	return req, nil
}

// do performs a request and, when out is non-nil, decodes the JSON body into it.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.wrapTransportError(path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return apiErrorFrom(path, resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return fmt.Errorf("ollama: decoding response from %s: %w", path, err)
	}
	return nil
}

// openStream performs a request and hands back the still-open response body,
// for endpoints that stream server-sent events.
func (c *Client) openStream(ctx context.Context, path string, body any) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, c.wrapTransportError(path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return nil, apiErrorFrom(path, resp)
	}
	return resp.Body, nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) postJSON(ctx context.Context, path string, in, out any) error {
	return c.do(ctx, http.MethodPost, path, in, out)
}

// wrapTransportError turns a connection-level failure into ErrUnavailable so
// callers can tell "server is down" from "server said no".
func (c *Client) wrapTransportError(path string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("ollama: %s: %w", path, err)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return fmt.Errorf("%w at %s: %w", ErrUnavailable, c.baseURL, err)
	}
	return fmt.Errorf("ollama: %s: %w", path, err)
}

// apiErrorFrom converts a non-2xx response into an *APIError, preferring the
// server's own explanation.
func apiErrorFrom(path string, resp *http.Response) error {
	apiErr := &APIError{StatusCode: resp.StatusCode, Endpoint: path}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err == nil && len(raw) > 0 {
		if message := parseErrorMessage(raw); message != "" {
			apiErr.Message = message
		} else {
			apiErr.Message = strings.TrimSpace(string(raw))
		}
	}
	return apiErr
}

// parseErrorMessage pulls the explanation out of an error body, accepting both
// shapes the two endpoints use: Ollama's {"error": "..."} and the
// OpenAI-compatible {"error": {"message": "..."}}.
func parseErrorMessage(raw []byte) string {
	var payload struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Error) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(payload.Error, &text); err == nil {
		return text
	}

	var object struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload.Error, &object); err == nil {
		return object.Message
	}
	return ""
}

// Version returns the server version string reported by /api/version.
func (c *Client) Version(ctx context.Context) (string, error) {
	var payload struct {
		Version string `json:"version"`
	}
	if err := c.getJSON(ctx, "/api/version", &payload); err != nil {
		return "", err
	}
	return payload.Version, nil
}
