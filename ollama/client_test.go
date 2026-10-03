package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// assertSameJSON compares two JSON documents ignoring key order and spacing.
func assertSameJSON(t *testing.T, want, got string) {
	t.Helper()

	var wantValue, gotValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("invalid expected JSON %s: %v", want, err)
	}
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("invalid actual JSON %s: %v", got, err)
	}
	wantCanonical, _ := json.Marshal(wantValue)
	gotCanonical, _ := json.Marshal(gotValue)
	if string(wantCanonical) != string(gotCanonical) {
		t.Fatalf("JSON mismatch\n want: %s\n  got: %s", wantCanonical, gotCanonical)
	}
}

func TestDecide(t *testing.T) {
	t.Parallel()

	var captured struct {
		Method string
		Path   string
		Body   map[string]any
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Method, captured.Path = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&captured.Body); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"nimble","answers":{"says_hello":{"type":"noul","noul":0.9452041191404016}},`+
			`"usage":{"input_tokens":162,"output_tokens":1}}`)
	}))
	defer server.Close()

	question, err := NewNoulQuestion("Does the state contain a greeting?", NoulCriteria{
		True:  "The state text contains a greeting.",
		False: "The state text does not contain a greeting.",
	})
	if err != nil {
		t.Fatalf("building question: %v", err)
	}

	client := New(WithBaseURL(server.URL))
	resp, err := client.Decide(context.Background(), DecideRequest{
		Model:     "nimble",
		State:     "Hola Mundo",
		Questions: map[string]Question{"says_hello": question},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	if captured.Method != http.MethodPost || captured.Path != "/v1/systemone" {
		t.Fatalf("got %s %s, want POST /v1/systemone", captured.Method, captured.Path)
	}
	if captured.Body["model"] != "nimble" {
		t.Errorf("model = %v, want nimble", captured.Body["model"])
	}
	if captured.Body["state"] != "Hola Mundo" {
		t.Errorf("state = %v, want Hola Mundo", captured.Body["state"])
	}
	assertSameJSON(t,
		`{"says_hello":{"type":"noul","instructions":"Does the state contain a greeting?",`+
			`"criteria":{"true":"The state text contains a greeting.","false":"The state text does not contain a greeting."}}}`,
		mustMarshal(t, captured.Body["questions"]),
	)

	answer, ok := resp.Answers["says_hello"].AsNoul()
	if !ok {
		t.Fatal("expected a noul answer")
	}
	if answer.Probability < 0.945 || answer.Probability > 0.946 {
		t.Errorf("probability = %v, want ~0.945", answer.Probability)
	}
	if resp.Usage.InputTokens != 162 || resp.Usage.OutputTokens != 1 {
		t.Errorf("usage = %+v, want 162/1", resp.Usage)
	}
}

// The client must reject a bad request locally rather than spending a round trip.
func TestDecideRejectsInvalidRequestWithoutCallingTheServer(t *testing.T) {
	t.Parallel()

	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	client := New(WithBaseURL(server.URL))
	_, err := client.Decide(context.Background(), DecideRequest{Model: "", Questions: map[string]Question{}})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
	if called {
		t.Error("the server should not have been called")
	}
}

func TestDecideSurfacesAPIErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		body       string
		wantMsg    string
		wantNotFnd bool
	}{
		{
			name:    "bad request with explanation",
			status:  http.StatusBadRequest,
			body:    `{"error":"model is required"}`,
			wantMsg: "model is required",
		},
		{
			name:       "unknown model",
			status:     http.StatusNotFound,
			body:       `{"error":"model \"nope\" not found"}`,
			wantMsg:    `model "nope" not found`,
			wantNotFnd: true,
		},
		{
			name:    "non json body",
			status:  http.StatusInternalServerError,
			body:    "boom",
			wantMsg: "boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			question := Question{
				Type:         QuestionNoul,
				Instructions: "Is it a greeting?",
				Criteria:     json.RawMessage(`{"true":"yes","false":"no"}`),
			}
			client := New(WithBaseURL(server.URL))
			_, err := client.Decide(context.Background(), DecideRequest{
				Model:     "nimble",
				State:     "Hola",
				Questions: map[string]Question{"greeting": question},
			})
			if err == nil {
				t.Fatal("expected an error, got none")
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected an *APIError, got %T: %v", err, err)
			}
			if apiErr.StatusCode != tt.status {
				t.Errorf("status = %d, want %d", apiErr.StatusCode, tt.status)
			}
			if apiErr.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", apiErr.Message, tt.wantMsg)
			}
			if apiErr.Endpoint != "/v1/systemone" {
				t.Errorf("endpoint = %q, want /v1/systemone", apiErr.Endpoint)
			}
			if got := IsNotFound(err); got != tt.wantNotFnd {
				t.Errorf("IsNotFound = %v, want %v", got, tt.wantNotFnd)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error text %q should mention %q", err, tt.wantMsg)
			}
		})
	}
}

func TestUnreachableServerReportsUnavailable(t *testing.T) {
	t.Parallel()

	// Port 1 on loopback refuses connections.
	client := New(WithBaseURL("http://127.0.0.1:1"))
	_, err := client.Decide(context.Background(), DecideRequest{
		Model:     "nimble",
		State:     "Hola",
		Questions: map[string]Question{"g": {Type: QuestionNoul, Instructions: "x", Criteria: json.RawMessage(`{}`)}},
	})
	if !IsUnavailable(err) {
		t.Fatalf("expected an unavailable error, got %v", err)
	}
}

func TestListModelsAndVersion(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"nimble:latest","object":"model","owned_by":"library"}]}`)
		case "/api/version":
			fmt.Fprint(w, `{"version":"0.35.0"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := New(WithBaseURL(server.URL))

	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "nimble:latest" {
		t.Fatalf("models = %+v, want one entry for nimble:latest", models)
	}

	version, err := client.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != "0.35.0" {
		t.Errorf("version = %q, want 0.35.0", version)
	}
}

func TestSendsConfiguredHeaders(t *testing.T) {
	t.Parallel()

	var auth, custom string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		custom = r.Header.Get("X-Trace")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"0.35.0"}`)
	}))
	defer server.Close()

	client := New(WithBaseURL(server.URL), WithAPIKey("secret"), WithHeader("X-Trace", "abc"))
	if _, err := client.Version(context.Background()); err != nil {
		t.Fatalf("Version: %v", err)
	}
	if auth != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer secret")
	}
	if custom != "abc" {
		t.Errorf("X-Trace = %q, want abc", custom)
	}
}

func TestWithBaseURLTrimsTrailingSlash(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"http://localhost:11434/":    "http://localhost:11434",
		"  http://host:1234//  ":     "http://host:1234",
		"":                           DefaultBaseURL,
		"   ":                        DefaultBaseURL,
		"https://api.example.com/v1": "https://api.example.com/v1",
	}
	for input, want := range cases {
		if got := New(WithBaseURL(input)).BaseURL(); got != want {
			t.Errorf("WithBaseURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}
