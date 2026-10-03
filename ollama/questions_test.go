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

func TestParseQuestions(t *testing.T) {
	t.Parallel()

	t.Run("un conjunto válido", func(t *testing.T) {
		t.Parallel()

		raw := []byte(`{
			"saludo":   {"type": "noul", "instructions": "¿Contiene un saludo?"},
			"tono":     {"type": "choice", "instructions": "¿Qué tono?", "criteria": {"a": "A", "b": "B"}},
			"calidad":  {"type": "score", "instructions": "¿Qué calidad?", "criteria": ["baja", "alta"]}
		}`)

		questions, err := ParseQuestions(raw)
		if err != nil {
			t.Fatalf("ParseQuestions: %v", err)
		}
		if len(questions) != 3 {
			t.Fatalf("got %d questions, want 3", len(questions))
		}
		if questions["saludo"].Type != QuestionNoul {
			t.Errorf("saludo type = %q", questions["saludo"].Type)
		}
		if len(questions["saludo"].Criteria) != 0 {
			t.Error("a noul question without criteria should carry none")
		}
		if questions["tono"].Type != QuestionChoice || questions["calidad"].Type != QuestionScore {
			t.Errorf("types = %q/%q", questions["tono"].Type, questions["calidad"].Type)
		}
	})

	t.Run("errores nombran la pregunta culpable", func(t *testing.T) {
		t.Parallel()

		// Varias preguntas malas a la vez: el error debe señalar una concreta.
		raw := []byte(`{
			"buena":  {"type": "noul", "instructions": "ok"},
			"mala_a": {"type": "oracle", "instructions": "?"},
			"mala_b": {"type": "choice", "instructions": "?", "criteria": {"solo": "única"}}
		}`)
		_, err := ParseQuestions(raw)
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected ErrInvalidRequest, got %v", err)
		}
		if !strings.Contains(err.Error(), `"mala_a"`) && !strings.Contains(err.Error(), `"mala_b"`) {
			t.Errorf("error should name the offending question, got %v", err)
		}
	})

	t.Run("el fallo es reproducible", func(t *testing.T) {
		t.Parallel()

		// Las claves se recorren ordenadas, así que dos preguntas malas
		// producen siempre el mismo error.
		raw := []byte(`{
			"zzz": {"type": "oracle", "instructions": "?"},
			"aaa": {"type": "choice", "instructions": "?", "criteria": {"x": "X"}}
		}`)

		var primero string
		for i := range 5 {
			_, err := ParseQuestions(raw)
			if err == nil {
				t.Fatal("expected an error")
			}
			if i == 0 {
				primero = err.Error()
				continue
			}
			if err.Error() != primero {
				t.Fatalf("error changed between runs:\n%v\n%v", primero, err)
			}
		}
		if !strings.Contains(primero, `"aaa"`) {
			t.Errorf("expected the alphabetically first bad question, got %v", primero)
		}
	})

	t.Run("conjuntos con tamaño inválido", func(t *testing.T) {
		t.Parallel()

		if _, err := ParseQuestions([]byte(`{}`)); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("an empty set should be rejected, got %v", err)
		}

		var muchas strings.Builder
		muchas.WriteString("{")
		for i := range MaxQuestions + 1 {
			if i > 0 {
				muchas.WriteString(",")
			}
			fmt.Fprintf(&muchas, `"p%d":{"type":"noul","instructions":"?"}`, i)
		}
		muchas.WriteString("}")
		if _, err := ParseQuestions([]byte(muchas.String())); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%d questions should be rejected, got %v", MaxQuestions+1, err)
		}
	})

	t.Run("no es un objeto", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{`[]`, `"texto"`, `{roto`, `null`} {
			if _, err := ParseQuestions([]byte(raw)); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("ParseQuestions(%s) should fail with ErrInvalidRequest, got %v", raw, err)
			}
		}
	})
}

func TestLoadQuestions(t *testing.T) {
	t.Parallel()

	reader := strings.NewReader(`{"q": {"type": "noul", "instructions": "¿ok?"}}`)
	questions, err := LoadQuestions(reader)
	if err != nil {
		t.Fatalf("LoadQuestions: %v", err)
	}
	if len(questions) != 1 {
		t.Fatalf("got %d questions, want 1", len(questions))
	}

	if _, err := LoadQuestions(strings.NewReader("{roto")); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("malformed input should fail, got %v", err)
	}
}

// DecideYesNo is the one-call path for the most common case.
func TestDecideYesNo(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"nimble","answers":{"q":{"type":"noul","noul":0.87}},`+
			`"usage":{"input_tokens":10,"output_tokens":1}}`)
	}))
	defer server.Close()

	question, err := NewNoulQuestion("¿Contiene un saludo?", NoulCriteria{})
	if err != nil {
		t.Fatalf("question: %v", err)
	}

	client := New(WithBaseURL(server.URL))
	answer, err := client.DecideYesNo(context.Background(), "nimble", StringState("Hola"), question)
	if err != nil {
		t.Fatalf("DecideYesNo: %v", err)
	}

	noul, ok := answer.AsNoul()
	if !ok {
		t.Fatalf("expected a noul answer, got %q", answer.Type)
	}
	if noul.Probability != 0.87 {
		t.Errorf("probability = %v, want 0.87", noul.Probability)
	}
	if captured["model"] != "nimble" {
		t.Errorf("model = %v", captured["model"])
	}
	questions, ok := captured["questions"].(map[string]any)
	if !ok || len(questions) != 1 {
		t.Errorf("expected exactly one question in the request, got %v", captured["questions"])
	}
}

// DecideYesNo must refuse a question of another type instead of sending it.
func TestDecideYesNoRejectsOtherTypes(t *testing.T) {
	t.Parallel()

	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	choice, err := NewChoiceQuestion("¿Cuál?", ChoiceCriteria{"a": "A", "b": "B"})
	if err != nil {
		t.Fatalf("choice question: %v", err)
	}

	_, err = New(WithBaseURL(server.URL)).DecideYesNo(context.Background(), "nimble", StringState("x"), choice)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
	if called {
		t.Error("the server should not have been called")
	}
}

func TestDecideYesNoPropagatesErrors(t *testing.T) {
	t.Parallel()

	question, err := NewNoulQuestion("¿ok?", NoulCriteria{})
	if err != nil {
		t.Fatalf("question: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model not found"}`)
	}))
	defer server.Close()

	_, err = New(WithBaseURL(server.URL)).DecideYesNo(context.Background(), "nimble", StringState("x"), question)
	if !IsNotFound(err) {
		t.Fatalf("expected a not-found error, got %v", err)
	}
}
