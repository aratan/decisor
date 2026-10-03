package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testRequest(state string, instructions string) DecideRequest {
	question, err := NewNoulQuestion(instructions, NoulCriteria{})
	if err != nil {
		panic(err)
	}
	return DecideRequest{
		Model:     "nimble",
		State:     StringState(state),
		Questions: map[string]Question{"q": question},
	}
}

// An oversized body must fail locally with an actionable message instead of
// costing a round trip and coming back as a bare 413.
func TestDecideRejectsOversizedBodyLocally(t *testing.T) {
	t.Parallel()

	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"nimble","answers":{},"usage":{}}`)
	}))
	defer server.Close()

	req := testRequest("Hola", "¿"+strings.Repeat("palabra ", 20000)+"?")
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(payload) <= MaxRequestBytes {
		t.Fatalf("test needs a body over the limit, got %d bytes", len(payload))
	}

	_, err = New(WithBaseURL(server.URL)).Decide(context.Background(), req)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
	if !strings.Contains(err.Error(), "64 KiB") {
		t.Errorf("error should mention the limit, got %v", err)
	}
	if called {
		t.Error("the server should not have been called")
	}
}

func TestDecideSendsTheSameBodyItValidated(t *testing.T) {
	t.Parallel()

	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"nimble","answers":{"q":{"type":"noul","noul":0.5}},"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	// Passing pre-marshalled bytes must not double-encode the payload.
	req := testRequest("Hola Mundo", "¿Contiene un saludo?")
	expected, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, err := New(WithBaseURL(server.URL)).Decide(context.Background(), req)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Model != "nimble" {
		t.Errorf("model = %q", resp.Model)
	}
	assertSameJSON(t, string(expected), string(captured))
}

func TestEstimatePromptTokensGrowsWithStateAndQuestions(t *testing.T) {
	t.Parallel()

	short := testRequest("Hola", "¿Es un saludo?").EstimatePromptTokens()
	longer := testRequest(strings.Repeat("una frase bastante larga. ", 200), "¿Es un saludo?").EstimatePromptTokens()
	if longer <= short {
		t.Fatalf("a longer state should estimate more tokens: %d vs %d", longer, short)
	}

	// The longest question sets the bound, since prompts are built per
	// question rather than pooled.
	base := testRequest("Hola", "¿Corto?")
	withLongInstructions := base
	long, err := NewNoulQuestion("?"+strings.Repeat("muy ", 500)+"?", NoulCriteria{})
	if err != nil {
		t.Fatalf("question: %v", err)
	}
	withLongInstructions.Questions = map[string]Question{"q": long}
	if withLongInstructions.EstimatePromptTokens() <= base.EstimatePromptTokens() {
		t.Error("longer instructions should raise the estimate")
	}
}

func TestApproxTokens(t *testing.T) {
	t.Parallel()

	cases := map[string]int{
		"":       0,
		"a":      1,
		"abcd":   1,
		"abcde":  2,
		"abcdef": 2,
	}
	for input, want := range cases {
		if got := ApproxTokens(input); got != want {
			t.Errorf("ApproxTokens(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestErrorClassifiers(t *testing.T) {
	t.Parallel()

	overflow := &APIError{
		StatusCode: http.StatusBadRequest,
		Message:    "prompt 0 has 9144 tokens; expected 1–8194 (input is never truncated)",
	}
	if !IsPromptOverflow(overflow) {
		t.Error("a context overflow should be recognised")
	}
	if IsNotFound(overflow) || IsTooLarge(overflow) {
		t.Error("a context overflow is neither not-found nor too-large")
	}

	tooLarge := &APIError{StatusCode: http.StatusRequestEntityTooLarge, Message: "request body must not exceed 64 KiB"}
	if !IsTooLarge(tooLarge) {
		t.Error("a 413 should be recognised as too large")
	}
	if IsPromptOverflow(tooLarge) {
		t.Error("a 413 is not a context overflow")
	}

	other := &APIError{StatusCode: http.StatusBadRequest, Message: "model is required"}
	if IsPromptOverflow(other) {
		t.Error("an unrelated 400 must not be read as an overflow")
	}

	if IsPromptOverflow(nil) || IsNotFound(nil) || IsTooLarge(nil) {
		t.Error("nil is not any of these")
	}
	if !IsPromptOverflow(fmt.Errorf("wrapped: %w", error(overflow))) {
		t.Error("a wrapped overflow should still be recognised")
	}
}
