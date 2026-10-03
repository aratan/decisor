package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReasoningEffortValid(t *testing.T) {
	t.Parallel()

	for _, effort := range []ReasoningEffort{
		ReasoningNone, ReasoningMinimal, ReasoningLow, ReasoningMedium,
		ReasoningHigh, ReasoningExtraHigh, ReasoningUltra, ReasoningMax,
	} {
		if !effort.Valid() {
			t.Errorf("%q should be valid", effort)
		}
	}
	for _, effort := range []ReasoningEffort{"", "turbo", "NONE", "off", "0"} {
		if effort.Valid() {
			t.Errorf("%q should be rejected", effort)
		}
	}
}

func TestReasoningEffortIsSentAndValidated(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = nil // reset, so a key absent from this body reads as absent
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"hola"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := New(WithBaseURL(server.URL))
	_, err := client.Chat(context.Background(), ChatRequest{
		Model:           "nimble",
		Messages:        []Message{Text("hola")},
		ReasoningEffort: ReasoningNone,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if captured["reasoning_effort"] != "none" {
		t.Fatalf("reasoning_effort = %v, want none", captured["reasoning_effort"])
	}

	// An empty effort must be omitted rather than sent as "".
	if _, err := client.Chat(context.Background(), ChatRequest{
		Model:    "nimble",
		Messages: []Message{Text("hola")},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, present := captured["reasoning_effort"]; present {
		t.Fatalf("reasoning_effort should be omitted when unset, got %v", captured["reasoning_effort"])
	}

	// An unknown level fails locally, before the round trip.
	_, err = client.Chat(context.Background(), ChatRequest{
		Model:           "nimble",
		Messages:        []Message{Text("hola")},
		ReasoningEffort: "turbo",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

// The OpenAI-compatible endpoint reports failures as {"error": {...}} while the
// native one uses {"error": "..."}; both have to yield a readable message.
func TestParseErrorMessageAcceptsBothShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "native ollama string",
			body: `{"error":"model is required"}`,
			want: "model is required",
		},
		{
			name: "openai object",
			body: `{"error":{"message":"invalid reasoning value: \"turbo\"","type":"invalid_request_error","param":null,"code":null}}`,
			want: `invalid reasoning value: "turbo"`,
		},
		{
			name: "not json",
			body: "boom",
			want: "",
		},
		{
			name: "no error field",
			body: `{"detail":"whatever"}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := parseErrorMessage([]byte(tt.body)); got != tt.want {
				t.Fatalf("parseErrorMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChatSurfacesOpenAIStyleError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"invalid reasoning value: \"turbo\"","type":"invalid_request_error"}}`))
	}))
	defer server.Close()

	_, err := New(WithBaseURL(server.URL)).Chat(context.Background(), ChatRequest{
		Model:    "nimble",
		Messages: []Message{Text("hola")},
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected an *APIError, got %T: %v", err, err)
	}
	if apiErr.Message != `invalid reasoning value: "turbo"` {
		t.Fatalf("message = %q, want the nested OpenAI message", apiErr.Message)
	}
}

func TestChatChunkFinishReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		chunk ChatChunk
		want  string
	}{
		{
			name:  "no choices yet",
			chunk: ChatChunk{},
			want:  "",
		},
		{
			name: "still generating",
			chunk: ChatChunk{Choices: []ChatChunkChoice{
				{Delta: Message{Content: "Ho"}},
			}},
			want: "",
		},
		{
			name: "stopped on the token budget",
			chunk: ChatChunk{Choices: []ChatChunkChoice{
				{Delta: Message{Reasoning: "pensando"}},
				{Delta: Message{}, FinishReason: "length"},
			}},
			want: "length",
		},
		{
			name: "natural ending",
			chunk: ChatChunk{Choices: []ChatChunkChoice{
				{FinishReason: "stop"},
			}},
			want: "stop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.chunk.FinishReason(); got != tt.want {
				t.Fatalf("FinishReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The reasoning trace and the visible text arrive on separate fields, and a
// chunk must never merge them.
func TestChatChunkSeparatesReasoningFromContent(t *testing.T) {
	t.Parallel()

	chunk := ChatChunk{Choices: []ChatChunkChoice{
		{Delta: Message{Content: "Hola", Reasoning: "primero pienso"}},
	}}
	if got := chunk.Text(); got != "Hola" {
		t.Errorf("Text() = %q, want Hola", got)
	}
	if got := chunk.Reasoning(); got != "primero pienso" {
		t.Errorf("Reasoning() = %q, want 'primero pienso'", got)
	}
}
