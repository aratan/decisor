package ollama

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

var (
	// ErrInvalidRequest reports a request rejected locally, before any call is
	// made, because it does not satisfy the server's documented constraints.
	ErrInvalidRequest = errors.New("invalid request")

	// ErrUnavailable reports that the Ollama server could not be reached at
	// all: wrong address, server not started, or connection refused.
	ErrUnavailable = errors.New("ollama server unavailable")
)

// APIError is returned when the server replies with a non-2xx status. Ollama
// reports failures as {"error": "..."} with JSON content type.
type APIError struct {
	StatusCode int
	// Endpoint is the path that failed, e.g. "/v1/systemone".
	Endpoint string
	// Message is the server-provided explanation, when it sent one.
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("ollama: %s returned HTTP %d", e.Endpoint, e.StatusCode)
	}
	return fmt.Sprintf("ollama: %s returned HTTP %d: %s", e.Endpoint, e.StatusCode, e.Message)
}

// Temporary reports whether retrying the same request could succeed.
func (e *APIError) Temporary() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// IsNotFound reports whether err is an APIError carrying HTTP 404, which is
// what the server returns for an unknown model.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// IsPromptOverflow reports whether err means a question prompt did not fit the
// model's context window. The server words this as
//
//	prompt 0 has 9144 tokens; expected 1-8194 (input is never truncated)
//
// which is matched on the message text, so it is only as stable as the
// server's wording.
func IsPromptOverflow(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}
	return strings.Contains(apiErr.Message, "never truncated") ||
		(strings.Contains(apiErr.Message, "tokens") && strings.Contains(apiErr.Message, "expected 1"))
}

// IsTooLarge reports whether err means the request body exceeded the server's
// 64 KiB limit.
func IsTooLarge(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusRequestEntityTooLarge
}

// IsUnavailable reports whether err means the server could not be reached.
func IsUnavailable(err error) bool {
	if errors.Is(err, ErrUnavailable) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
