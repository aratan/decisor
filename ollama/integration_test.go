package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Integration tests run against a real Ollama server. They are skipped unless
// SYSTEMONE_E2E is set, so `go test ./...` stays hermetic:
//
//	SYSTEMONE_E2E=1 go test ./ollama -run Integration -v
//
// Point SYSTEMONE_URL and SYSTEMONE_MODEL at a non-default server or model.
func integrationClient(t *testing.T) *Client {
	t.Helper()

	if os.Getenv("SYSTEMONE_E2E") == "" {
		t.Skip("set SYSTEMONE_E2E=1 to run tests against a live Ollama server")
	}

	client := New(WithBaseURL(envOr("SYSTEMONE_URL", DefaultBaseURL)))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := client.Version(ctx); err != nil {
		t.Skipf("no Ollama server reachable: %v", err)
	}
	return client
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// integrationModel returns the model under test, skipping when it is not
// installed on the server.
func integrationModel(t *testing.T, client *Client) string {
	t.Helper()

	model := envOr("SYSTEMONE_MODEL", "nimble")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		t.Fatalf("listing models: %v", err)
	}
	for _, candidate := range models {
		// The server reports "nimble:latest" for a model requested as
		// "nimble", so compare the tag-less part too.
		if candidate.ID == model || strings.SplitN(candidate.ID, ":", 2)[0] == model {
			return model
		}
	}
	t.Skipf("model %q is not installed on the test server", model)
	return ""
}

// TestIntegrationDecide exercises every question type in a single call, which
// is the behaviour the endpoint is built around.
func TestIntegrationDecide(t *testing.T) {
	client := integrationClient(t)
	model := integrationModel(t, client)

	greeting, err := NewNoulQuestion(
		"Does the state text contain a greeting?",
		NoulCriteria{
			True:  "The state text contains a greeting.",
			False: "The state text does not contain a greeting.",
		},
	)
	if err != nil {
		t.Fatalf("noul question: %v", err)
	}

	tone, err := NewChoiceQuestion(
		"What is the overall tone of the state text?",
		ChoiceCriteria{
			"greeting": "The state text is a greeting.",
			"farewell": "The state text says goodbye.",
			"other":    "The state text is none of the above.",
		},
	)
	if err != nil {
		t.Fatalf("choice question: %v", err)
	}

	politeness, err := NewScoreQuestion(
		"How polite is the state text?",
		ScoreCriteria{"rude", "polite", "very polite"},
	)
	if err != nil {
		t.Fatalf("score question: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	resp, err := client.Decide(ctx, DecideRequest{
		Model: model,
		State: StringState("Hola, ¿cómo estás? Me llamo Ana."),
		Questions: map[string]Question{
			"says_greeting": greeting,
			"tone":          tone,
			"politeness":    politeness,
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	if len(resp.Answers) != 3 {
		t.Fatalf("got %d answers, want 3: %+v", len(resp.Answers), resp.Answers)
	}
	if len(resp.Order) != 3 {
		t.Errorf("Order has %d entries, want 3: %v", len(resp.Order), resp.Order)
	}
	if resp.Usage.InputTokens == 0 {
		t.Error("expected a non-zero input token count")
	}

	if noul, ok := resp.Answers["says_greeting"].AsNoul(); !ok {
		t.Error("says_greeting should be a noul answer")
	} else if !noul.Verdict(0.5) {
		t.Errorf("a greeting was not detected: p=%v", noul.Probability)
	}

	if choice, ok := resp.Answers["tone"].AsChoice(); !ok {
		t.Error("tone should be a choice answer")
	} else if choice.Selected != "greeting" {
		t.Errorf("tone = %q, want greeting", choice.Selected)
	}

	if score, ok := resp.Answers["politeness"].AsScore(); !ok {
		t.Error("politeness should be a score answer")
	} else if index, _, ok := score.Bucket(); !ok {
		t.Error("politeness should expose a bucket")
	} else if index == 0 {
		t.Errorf("a polite greeting scored %d (rude)", index)
	}
}

// TestIntegrationNoulWithoutCriteria covers the documented optionality of a
// noul question's criteria, which must be omitted rather than sent as null.
func TestIntegrationNoulWithoutCriteria(t *testing.T) {
	client := integrationClient(t)
	model := integrationModel(t, client)

	bare, err := NewNoulQuestion("Does the state text contain a greeting?", NoulCriteria{})
	if err != nil {
		t.Fatalf("noul question: %v", err)
	}
	if len(bare.Criteria) != 0 {
		t.Fatalf("criteria = %s, want none so the key is omitted", bare.Criteria)
	}

	described, err := NewNoulQuestion(
		"Does the state text contain a greeting?",
		NoulCriteria{True: "It contains a greeting.", False: "It does not."},
	)
	if err != nil {
		t.Fatalf("described noul question: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := client.Decide(ctx, DecideRequest{
		Model: model,
		State: StringState("Hola Mundo"),
		Questions: map[string]Question{
			"bare":      bare,
			"described": described,
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	for _, name := range []string{"bare", "described"} {
		noul, ok := resp.Answers[name].AsNoul()
		if !ok {
			t.Fatalf("%s should be a noul answer", name)
		}
		if !noul.Verdict(0.5) {
			t.Errorf("%s: a greeting was not detected: p=%v", name, noul.Probability)
		}
	}
}

// TestIntegrationStructuredState checks that the server accepts a JSON object
// as the state, not only plain text.
func TestIntegrationStructuredState(t *testing.T) {
	client := integrationClient(t)
	model := integrationModel(t, client)

	greeting, err := NewNoulQuestion("Does the message contain a greeting?", NoulCriteria{})
	if err != nil {
		t.Fatalf("noul question: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	states := map[string]State{
		"object": map[string]any{"author": "Ana", "message": "Hola Mundo"},
		"array":  []string{"Hola Mundo"},
	}
	for name, state := range states {
		t.Run(name, func(t *testing.T) {
			resp, err := client.Decide(ctx, DecideRequest{
				Model:     model,
				State:     state,
				Questions: map[string]Question{"greeting": greeting},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			noul, ok := resp.Answers["greeting"].AsNoul()
			if !ok {
				t.Fatal("expected a noul answer")
			}
			if !noul.Verdict(0.5) {
				t.Errorf("greeting not detected in a %s state: p=%v", name, noul.Probability)
			}
		})
	}
}

// TestIntegrationAnswersKeepOrder checks the guarantee that the server answers
// in the order the questions were sent.
func TestIntegrationAnswersKeepOrder(t *testing.T) {
	client := integrationClient(t)
	model := integrationModel(t, client)

	names := []string{"primero", "segundo", "tercero"}
	questions := make(map[string]Question, len(names))
	for _, name := range names {
		question, err := NewNoulQuestion("Does the state contain a greeting?", NoulCriteria{})
		if err != nil {
			t.Fatalf("noul question: %v", err)
		}
		questions[name] = question
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := client.Decide(ctx, DecideRequest{Model: model, State: "Hola Mundo", Questions: questions})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	if len(resp.Order) != len(names) {
		t.Fatalf("Order = %v, want %d entries", resp.Order, len(names))
	}
	ordered := resp.AnswersInOrder()
	if len(ordered) != len(names) {
		t.Fatalf("got %d ordered answers, want %d", len(ordered), len(names))
	}
	// The request is a Go map, so the wire order is whatever the encoder
	// produced; the response must echo that same sequence.
	if len(resp.Order) == len(ordered) {
		for i := range ordered {
			if ordered[i].Name != resp.Order[i] {
				t.Errorf("answer %d = %q, want %q", i, ordered[i].Name, resp.Order[i])
			}
		}
	}
	t.Logf("server order: %v", resp.Order)
}

// TestIntegrationPromptOverflow checks the per-question prompt budget: the
// state is repeated into every question's prompt and input is never truncated.
func TestIntegrationPromptOverflow(t *testing.T) {
	client := integrationClient(t)
	model := integrationModel(t, client)

	// Comfortably past 8194 tokens, but a body still under 64 KiB. The real
	// tokenizer counts roughly 5.7 characters per token on this text, so
	// ~58 KB of state lands around 10k tokens.
	state := strings.Repeat("El usuario escribe un mensaje. ", 2000)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := client.Decide(ctx, DecideRequest{
		Model:     model,
		State:     StringState(state),
		Questions: map[string]Question{"q": {Type: QuestionNoul, Instructions: "¿Es un mensaje?"}},
	})
	if err == nil {
		t.Fatal("expected the oversized prompt to be rejected")
	}
	if !IsPromptOverflow(err) {
		t.Fatalf("expected a prompt-overflow error, got %v", err)
	}

	// The estimate should also have flagged it, even though it overshoots.
	req := DecideRequest{
		Model:     model,
		State:     StringState(state),
		Questions: map[string]Question{"q": {Type: QuestionNoul, Instructions: "¿Es un mensaje?"}},
	}
	if estimate := req.EstimatePromptTokens(); estimate <= NimbleContextTokens {
		t.Errorf("estimate = %d, expected it to exceed the %d budget", estimate, NimbleContextTokens)
	}
}

// TestIntegrationOversizedBody checks the 64 KiB request limit, which the
// client is expected to catch before the round trip.
func TestIntegrationOversizedBody(t *testing.T) {
	client := integrationClient(t)
	// The model is irrelevant here because the request is rejected locally,
	// but resolving it skips the test when the server has nothing installed.
	_ = integrationModel(t, client)

	req := testRequest(strings.Repeat("palabra ", 30000), "¿Contiene un saludo?")
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(payload) <= MaxRequestBytes {
		t.Fatalf("test needs a body over %d bytes, got %d", MaxRequestBytes, len(payload))
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	_, err = client.Decide(ctx, req)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected a local ErrInvalidRequest, got %v", err)
	}
	if !strings.Contains(err.Error(), "64 KiB") {
		t.Errorf("error should name the limit, got %v", err)
	}
}

// TestIntegrationUnknownModel checks that a missing model surfaces as an
// *APIError rather than an opaque failure.
func TestIntegrationUnknownModel(t *testing.T) {
	client := integrationClient(t)

	question, err := NewNoulQuestion("Is it true?", NoulCriteria{True: "yes", False: "no"})
	if err != nil {
		t.Fatalf("noul question: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	_, err = client.Decide(ctx, DecideRequest{
		Model:     "definitely-not-installed",
		State:     StringState("Hola"),
		Questions: map[string]Question{"q": question},
	})
	if err == nil {
		t.Fatal("expected an error for an unknown model")
	}
	if !IsNotFound(err) && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a not-found error, got %v", err)
	}
}
