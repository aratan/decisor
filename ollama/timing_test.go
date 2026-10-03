package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Duration is measured by the client and must not leak into the API shape.
func TestDecideMeasuresDurationWithoutChangingJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"nimble","answers":{"q":{"type":"noul","noul":0.5}},"usage":{"input_tokens":9,"output_tokens":1}}`)
	}))
	defer server.Close()

	question, err := NewNoulQuestion("Is it a greeting?", NoulCriteria{})
	if err != nil {
		t.Fatalf("question: %v", err)
	}
	resp, err := New(WithBaseURL(server.URL)).Decide(context.Background(), DecideRequest{
		Model:     "nimble",
		State:     StringState("Hola"),
		Questions: map[string]Question{"q": question},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Duration < 20*time.Millisecond {
		t.Errorf("Duration = %s, expected at least the 20ms the server slept", resp.Duration)
	}

	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := decoded["Duration"]; present {
		t.Error("Duration should not appear in the JSON payload")
	}
}

func TestChatStreamStats(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		time.Sleep(15 * time.Millisecond) // delay before the first event
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"reasoning":"pienso"}}]}`+"\n\n")
		time.Sleep(10 * time.Millisecond)
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"Hola mundo"}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":" entero"}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	stats, err := New(WithBaseURL(server.URL)).ChatStreamWithStats(context.Background(), ChatRequest{
		Model:    "nimble",
		Messages: []Message{Text("hola")},
	}, func(ChatChunk) error { return nil })
	if err != nil {
		t.Fatalf("ChatStreamWithStats: %v", err)
	}

	if stats.Content != "Hola mundo entero" {
		t.Errorf("Content = %q", stats.Content)
	}
	if stats.Reasoning != "pienso" {
		t.Errorf("Reasoning = %q", stats.Reasoning)
	}
	if stats.Chunks != 4 {
		t.Errorf("Chunks = %d, want 4", stats.Chunks)
	}
	if stats.FirstToken < 15*time.Millisecond {
		t.Errorf("FirstToken = %s, expected at least the initial 15ms delay", stats.FirstToken)
	}
	if stats.Duration < stats.FirstToken {
		t.Errorf("Duration %s should not be shorter than FirstToken %s", stats.Duration, stats.FirstToken)
	}
	if rate := stats.TokensPerSecond(); rate <= 0 {
		t.Errorf("TokensPerSecond = %v, want a positive rate", rate)
	}
}

// A stream with no text at all has no first token and no token rate.
func TestStreamStatsWithoutContent(t *testing.T) {
	t.Parallel()

	stats := &StreamStats{Duration: time.Second}
	if stats.FirstToken != 0 {
		t.Errorf("FirstToken = %s, want 0", stats.FirstToken)
	}
	if rate := stats.TokensPerSecond(); rate != 0 {
		t.Errorf("TokensPerSecond = %v, want 0", rate)
	}
	if rate := (&StreamStats{}).TokensPerSecond(); rate != 0 {
		t.Errorf("TokensPerSecond on a zero stat = %v, want 0", rate)
	}
}

// Stats must survive a handler error, so callers can report partial progress.
func TestStreamStatsSurviveHandlerError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"parcial"}}]}`+"\n\n")
	}))
	defer server.Close()

	stats, err := New(WithBaseURL(server.URL)).ChatStreamWithStats(context.Background(), ChatRequest{
		Model:    "nimble",
		Messages: []Message{Text("hola")},
	}, func(ChatChunk) error { return context.Canceled })
	if err == nil {
		t.Fatal("expected the handler error to propagate")
	}
	if stats == nil {
		t.Fatal("stats should be returned even on error")
	}
	if stats.Content != "parcial" {
		t.Errorf("Content = %q, want the partial output", stats.Content)
	}
	if stats.Duration <= 0 {
		t.Error("Duration should be recorded even when the handler aborts")
	}
}

// ChatStream keeps its original signature by delegating to the stats variant.
func TestChatStreamDelegatesToStats(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer server.Close()

	resp, err := New(WithBaseURL(server.URL)).Chat(context.Background(), ChatRequest{
		Model:    "nimble",
		Messages: []Message{Text("hola")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Duration <= 0 {
		t.Error("Chat should record a duration")
	}
	if resp.Text() != "ok" {
		t.Errorf("Text() = %q", resp.Text())
	}
}
