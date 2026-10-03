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
