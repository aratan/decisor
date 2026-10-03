package ollama

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestQuestionMarshalling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		build   func() (Question, error)
		want    string
		wantErr bool
	}{
		{
			name: "noul",
			build: func() (Question, error) {
				return NewNoulQuestion("Is it a greeting?", NoulCriteria{
					True:  "It is a greeting.",
					False: "It is not a greeting.",
				})
			},
			want: `{"type":"noul","instructions":"Is it a greeting?",` +
				`"criteria":{"true":"It is a greeting.","false":"It is not a greeting."}}`,
		},
		{
			name: "choice",
			build: func() (Question, error) {
				return NewChoiceQuestion("What tone?", ChoiceCriteria{
					"greeting": "It is a greeting.",
					"other":    "Something else.",
				})
			},
			want: `{"type":"choice","instructions":"What tone?",` +
				`"criteria":{"greeting":"It is a greeting.","other":"Something else."}}`,
		},
		{
			name: "score",
			build: func() (Question, error) {
				return NewScoreQuestion("How polite?", ScoreCriteria{"rude", "polite"})
			},
			want: `{"type":"score","instructions":"How polite?","criteria":["rude","polite"]}`,
		},
		{
			name: "noul describing only the true side",
			build: func() (Question, error) {
				return NewNoulQuestion("Is it a greeting?", NoulCriteria{True: "It is a greeting."})
			},
			want: `{"type":"noul","instructions":"Is it a greeting?",` +
				`"criteria":{"true":"It is a greeting."}}`,
		},
		{
			// Criteria are optional for noul: the key must be absent, never
			// an explicit null, which the server rejects.
			name: "noul without criteria",
			build: func() (Question, error) {
				return NewNoulQuestion("Is it a greeting?", NoulCriteria{})
			},
			want: `{"type":"noul","instructions":"Is it a greeting?"}`,
		},
		{
			name: "choice with a single option",
			build: func() (Question, error) {
				return NewChoiceQuestion("Pick one", ChoiceCriteria{"only": "The only option."})
			},
			wantErr: true,
		},
		{
			name: "score with an empty scale",
			build: func() (Question, error) {
				return NewScoreQuestion("Rate it", ScoreCriteria{})
			},
			wantErr: true,
		},
		{
			name: "empty instructions",
			build: func() (Question, error) {
				return NewScoreQuestion("   ", ScoreCriteria{"low"})
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			question, err := tt.build()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got none")
				}
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("error should wrap ErrInvalidRequest, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := json.Marshal(question)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// Compare as decoded maps: choice keys have no guaranteed order.
			assertSameJSON(t, tt.want, string(got))
		})
	}
}

func TestChoiceQuestionRejectsTooManyOptions(t *testing.T) {
	t.Parallel()

	criteria := make(ChoiceCriteria, MaxChoiceOptions+1)
	for i := 0; i < MaxChoiceOptions+1; i++ {
		criteria[fmt.Sprintf("option-%02d", i)] = "description"
	}
	if _, err := NewChoiceQuestion("Pick one", criteria); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestParseQuestion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{
			name: "noul",
			raw:  `{"type":"noul","instructions":"Hola?","criteria":{"true":"sí","false":"no"}}`,
			want: `{"type":"noul","instructions":"Hola?","criteria":{"true":"sí","false":"no"}}`,
		},
		{
			name: "score",
			raw:  `{"type":"score","instructions":"Rate","criteria":["low","high"]}`,
			want: `{"type":"score","instructions":"Rate","criteria":["low","high"]}`,
		},
		{
			name:    "unknown type",
			raw:     `{"type":"oracle","instructions":"?","criteria":null}`,
			wantErr: true,
		},
		{
			name:    "missing type",
			raw:     `{"instructions":"?","criteria":null}`,
			wantErr: true,
		},
		{
			name:    "score criteria as an object",
			raw:     `{"type":"score","instructions":"?","criteria":{"low":"low"}}`,
			wantErr: true,
		},
		{
			name:    "noul criteria as an array",
			raw:     `{"type":"noul","instructions":"?","criteria":["yes","no"]}`,
			wantErr: true,
		},
		{
			name:    "malformed json",
			raw:     `{"type":`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			question, err := ParseQuestion(json.RawMessage(tt.raw))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got none")
				}
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("error should wrap ErrInvalidRequest, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := json.Marshal(question)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			assertSameJSON(t, tt.want, string(got))
		})
	}
}

func TestAnswerUnmarshalByType(t *testing.T) {
	t.Parallel()

	t.Run("noul", func(t *testing.T) {
		t.Parallel()

		var answer Answer
		raw := `{"type":"noul","noul":0.9452041191404016}`
		if err := json.Unmarshal([]byte(raw), &answer); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		noul, ok := answer.AsNoul()
		if !ok {
			t.Fatal("expected a noul answer")
		}
		if noul.Probability < 0.945 || noul.Probability > 0.946 {
			t.Fatalf("probability = %v, want ~0.945", noul.Probability)
		}
		if !noul.Verdict(0) {
			t.Fatal("0.945 should pass the default 0.5 threshold")
		}
		if _, ok := answer.AsChoice(); ok {
			t.Error("AsChoice should reject a noul answer")
		}
		if _, ok := answer.AsScore(); ok {
			t.Error("AsScore should reject a noul answer")
		}
	})

	t.Run("choice", func(t *testing.T) {
		t.Parallel()

		var answer Answer
		raw := `{"type":"choice","choice":"greeting",` +
			`"probabilities":{"greeting":0.9733042257385106,"other":0.026695774261489308},` +
			`"confidence":0.8224595070417408}`
		if err := json.Unmarshal([]byte(raw), &answer); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		choice, ok := answer.AsChoice()
		if !ok {
			t.Fatal("expected a choice answer")
		}
		if choice.Selected != "greeting" {
			t.Fatalf("selected = %q, want %q", choice.Selected, "greeting")
		}
		top, probability := choice.Top()
		if top != "greeting" || probability < 0.97 {
			t.Fatalf("Top() = %q/%v, want greeting/~0.973", top, probability)
		}
	})

	t.Run("score", func(t *testing.T) {
		t.Parallel()

		var answer Answer
		raw := `{"type":"score","score":1.0651983266910545,` +
			`"legend":{"0":"rude","1":"polite","2":"very polite"},` +
			`"probabilities":{"0":0.03592856362265351,"1":0.8629445460636386,"2":0.10112689031370793},` +
			`"confidence":0.5645154616669567}`
		if err := json.Unmarshal([]byte(raw), &answer); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		score, ok := answer.AsScore()
		if !ok {
			t.Fatal("expected a score answer")
		}
		index, label, ok := score.Bucket()
		if !ok {
			t.Fatal("expected a bucket")
		}
		if index != 1 || label != "polite" {
			t.Fatalf("Bucket() = %d/%q, want 1/polite", index, label)
		}
	})
}

// An answer must round-trip through the API's own shape, not through Go field
// names, and must keep values that happen to be zero.
func TestAnswerMarshalRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "noul", raw: `{"type":"noul","noul":0.9452041191404016}`},
		{name: "noul at zero", raw: `{"type":"noul","noul":0}`},
		{
			name: "choice",
			raw:  `{"type":"choice","choice":"greeting","probabilities":{"greeting":0.97,"other":0.03},"confidence":0.82}`,
		},
		{
			name: "score",
			raw: `{"type":"score","score":1.0651983266910545,"legend":{"0":"rude","1":"polite"},` +
				`"probabilities":{"0":0.03,"1":0.86},"confidence":0.56}`,
		},
		{name: "score at zero", raw: `{"type":"score","score":0,"legend":{"0":"low"},"probabilities":{"0":1}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var answer Answer
			if err := json.Unmarshal([]byte(tt.raw), &answer); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			encoded, err := json.Marshal(answer)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			assertSameJSON(t, tt.raw, string(encoded))
		})
	}
}

// A score of exactly zero must not be confused with an absent value.
func TestAnswerUnmarshalKeepsZeroValues(t *testing.T) {
	t.Parallel()

	var answer Answer
	raw := `{"type":"score","score":0,"probabilities":{"0":0.9,"1":0.1},"legend":{"0":"low","1":"high"}}`
	if err := json.Unmarshal([]byte(raw), &answer); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	score, _ := answer.AsScore()
	if score.Value != 0 {
		t.Fatalf("Value = %v, want 0", score.Value)
	}
	if index, _, _ := score.Bucket(); index != 0 {
		t.Fatalf("Bucket() index = %d, want 0", index)
	}
}

func TestChoiceAnswerTopBreaksTiesDeterministically(t *testing.T) {
	t.Parallel()

	answer := ChoiceAnswer{Probabilities: map[string]float64{
		"zebra": 0.5,
		"alpha": 0.5,
		"mid":   0.5,
	}}
	top, probability := answer.Top()
	if top != "alpha" {
		t.Fatalf("Top() = %q, want alpha (lowest key on a tie)", top)
	}
	if probability != 0.5 {
		t.Fatalf("probability = %v, want 0.5", probability)
	}
}

func TestScoreAnswerBucketWithoutProbabilities(t *testing.T) {
	t.Parallel()

	if _, _, ok := (ScoreAnswer{}).Bucket(); ok {
		t.Fatal("Bucket() should report false when there are no probabilities")
	}
}

func TestDecideRequestValidate(t *testing.T) {
	t.Parallel()

	valid := func() map[string]Question {
		return map[string]Question{
			"greeting": {
				Type:         QuestionNoul,
				Instructions: "Is it a greeting?",
				Criteria:     json.RawMessage(`{"true":"yes","false":"no"}`),
			},
		}
	}

	tests := []struct {
		name    string
		request DecideRequest
		wantErr bool
	}{
		{name: "valid", request: DecideRequest{Model: "nimble", State: StringState("Hola"), Questions: valid()}},
		{name: "missing model", request: DecideRequest{State: "Hola", Questions: valid()}, wantErr: true},
		{name: "no questions", request: DecideRequest{Model: "nimble", Questions: map[string]Question{}}, wantErr: true},
		{
			name:    "unknown question type",
			request: DecideRequest{Model: "nimble", Questions: map[string]Question{"q": {Type: "oracle", Instructions: "x"}}},
			wantErr: true,
		},
		{
			// choice always needs criteria, even though noul does not.
			name: "choice without criteria",
			request: DecideRequest{Model: "nimble", Questions: map[string]Question{
				"q": {Type: QuestionChoice, Instructions: "x"},
			}},
			wantErr: true,
		},
		{
			name:    "noul without criteria is allowed",
			request: DecideRequest{Model: "nimble", State: "Hola", Questions: map[string]Question{"q": {Type: QuestionNoul, Instructions: "x"}}},
		},
		{
			name:    "missing state",
			request: DecideRequest{Model: "nimble", Questions: valid()},
			wantErr: true,
		},
		{
			name:    "too many questions",
			request: DecideRequest{Model: "nimble", Questions: manyQuestions(MaxQuestions + 1)},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.request.Validate()
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("expected ErrInvalidRequest, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func manyQuestions(n int) map[string]Question {
	questions := make(map[string]Question, n)
	for i := range n {
		questions[string(rune('a'+i%26))+string(rune('0'+i/26))] = Question{
			Type:         QuestionNoul,
			Instructions: "Is it true?",
			Criteria:     json.RawMessage(`{"true":"yes","false":"no"}`),
		}
	}
	return questions
}
