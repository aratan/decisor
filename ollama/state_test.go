package ollama

import (
	"encoding/json"
	"testing"
)

// The server accepts a string, an object or an array as the state, so the
// client must not force every caller through a string.
func TestDecideRequestMarshalsStructuredState(t *testing.T) {
	t.Parallel()

	question, err := NewNoulQuestion("Does the text contain a greeting?", NoulCriteria{})
	if err != nil {
		t.Fatalf("question: %v", err)
	}

	tests := []struct {
		name  string
		state State
		want  string
	}{
		{name: "string", state: StringState("Hola Mundo"), want: `"Hola Mundo"`},
		{name: "object", state: map[string]any{"autor": "Ana", "texto": "Hola"}, want: `{"autor":"Ana","texto":"Hola"}`},
		{name: "array", state: []string{"uno", "dos"}, want: `["uno","dos"]`},
		{name: "raw bytes", state: JSONState([]byte(`{"a":1}`)), want: `{"a":1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := DecideRequest{
				Model:     "nimble",
				State:     tt.state,
				Questions: map[string]Question{"q": question},
			}
			if err := req.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}

			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded struct {
				State json.RawMessage `json:"state"`
			}
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			assertSameJSON(t, tt.want, string(decoded.State))
		})
	}
}

// A noul question with no criteria must omit the key, never send a null.
func TestNoulQuestionWithoutCriteriaOmitsTheKey(t *testing.T) {
	t.Parallel()

	question, err := NewNoulQuestion("Is it a greeting?", NoulCriteria{})
	if err != nil {
		t.Fatalf("NewNoulQuestion: %v", err)
	}
	if question.Criteria != nil {
		t.Fatalf("criteria = %s, want nil so the key is omitted", question.Criteria)
	}

	raw, err := json.Marshal(question)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `{"type":"noul","instructions":"Is it a greeting?"}` {
		t.Fatalf("marshalled = %s", raw)
	}
}

// The server answers in the order the questions were sent; AnswersInOrder must
// replay that sequence even though the answers live in a Go map.
func TestAnswersInOrderFollowsServerOrder(t *testing.T) {
	t.Parallel()

	const payload = `{"model":"nimble","answers":{` +
		`"zzz_last":{"type":"noul","noul":0.9},` +
		`"aaa_first":{"type":"noul","noul":0.1},` +
		`"mmm_middle":{"type":"choice","choice":"b","confidence":0.5}},` +
		`"usage":{"input_tokens":10,"output_tokens":3}}`

	var resp DecideResponse
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantOrder := []string{"zzz_last", "aaa_first", "mmm_middle"}
	if len(resp.Order) != len(wantOrder) {
		t.Fatalf("Order = %v, want %v", resp.Order, wantOrder)
	}
	for i, name := range wantOrder {
		if resp.Order[i] != name {
			t.Fatalf("Order[%d] = %q, want %q", i, resp.Order[i], name)
		}
	}

	ordered := resp.AnswersInOrder()
	if len(ordered) != 3 {
		t.Fatalf("got %d ordered answers, want 3", len(ordered))
	}
	for i, named := range ordered {
		if named.Name != wantOrder[i] {
			t.Errorf("answer %d = %q, want %q", i, named.Name, wantOrder[i])
		}
	}
	if ordered[0].Answer.Noul != 0.9 {
		t.Errorf("first answer probability = %v, want 0.9", ordered[0].Answer.Noul)
	}
	if ordered[2].Answer.Confidence != 0.5 {
		t.Errorf("third answer confidence = %v, want 0.5", ordered[2].Answer.Confidence)
	}
}

// A response that omits a question must not yield a blank entry.
func TestAnswersInOrderSkipsMissingAnswers(t *testing.T) {
	t.Parallel()

	resp := DecideResponse{
		Order:   []string{"present", "absent"},
		Answers: map[string]Answer{"present": {Type: QuestionNoul, Noul: 0.5}},
	}
	ordered := resp.AnswersInOrder()
	if len(ordered) != 1 || ordered[0].Name != "present" {
		t.Fatalf("AnswersInOrder() = %+v, want just the present answer", ordered)
	}
}

// Without a recorded order the listing still has to be deterministic.
func TestAnswersInOrderFallsBackToSortedNames(t *testing.T) {
	t.Parallel()

	resp := DecideResponse{Answers: map[string]Answer{
		"zebra": {Type: QuestionNoul},
		"alpha": {Type: QuestionNoul},
	}}
	ordered := resp.AnswersInOrder()
	if len(ordered) != 2 || ordered[0].Name != "alpha" || ordered[1].Name != "zebra" {
		t.Fatalf("AnswersInOrder() = %+v, want alpha then zebra", ordered)
	}
}

func TestAnswerOrderIgnoresMalformedAnswers(t *testing.T) {
	t.Parallel()

	// A response whose "answers" is not an object must still decode the rest
	// without panicking.
	const payload = `{"model":"nimble","answers":"nope","usage":{"input_tokens":1,"output_tokens":1}}`
	var resp DecideResponse
	if err := json.Unmarshal([]byte(payload), &resp); err == nil {
		t.Fatal("expected an error decoding a non-object answers field")
	}

	const emptyAnswers = `{"model":"nimble","answers":{},"usage":{"input_tokens":1,"output_tokens":1}}`
	var ok DecideResponse
	if err := json.Unmarshal([]byte(emptyAnswers), &ok); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ok.AnswersInOrder()) != 0 {
		t.Fatalf("expected no answers, got %+v", ok.AnswersInOrder())
	}
}
