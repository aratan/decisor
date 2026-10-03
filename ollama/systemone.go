package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// QuestionType selects how a question is answered. The server accepts exactly
// these three values.
type QuestionType string

const (
	// QuestionNoul is a yes/no decision. The answer is the probability that
	// the true criteria holds. (The upstream API really does spell it "noul".)
	QuestionNoul QuestionType = "noul"
	// QuestionChoice picks one key from a map of options and reports the
	// probability of each.
	QuestionChoice QuestionType = "choice"
	// QuestionScore picks a position on an ordered scale; criteria is an
	// array whose indices are the scale values, starting at zero.
	QuestionScore QuestionType = "score"
)

const (
	// MinQuestions and MaxQuestions bound the questions map per call.
	MinQuestions = 1
	MaxQuestions = 64
	// MinChoiceOptions and MaxChoiceOptions bound a choice question.
	MinChoiceOptions = 2
	MaxChoiceOptions = 26
	// MaxRequestBytes is the largest request body the server accepts. Larger
	// bodies are rejected with HTTP 413.
	MaxRequestBytes = 64 << 10 // 64 KiB
	// NimbleContextTokens is the per-question prompt budget the server
	// enforces: every prompt must fall between 1 and 8194 tokens, and input
	// is never silently truncated.
	//
	// Each question is scored against the *whole* state, so a long state is
	// paid for once per question rather than once per call. A state that
	// barely fits one question still fits all of them, but a state that
	// overflows the budget fails immediately.
	NimbleContextTokens = 8194
)

// ApproxTokens estimates a token count at roughly four characters per token.
// It is a heuristic rather than a tokenizer: use it to catch a state that is
// far too long before spending a round trip, and let the server be the
// authority on the real count.
func ApproxTokens(s string) int { return (len(s) + 3) / 4 }

// NoulCriteria describes the two sides of a yes/no question so the model does
// not have to infer them from the instructions. Both fields are optional: pass
// a zero NoulCriteria to let the model judge from the instructions alone.
//
// Note that an empty NoulCriteria makes the client omit the "criteria" key
// entirely. Sending an explicit JSON null is rejected by the server.
type NoulCriteria struct {
	True  string `json:"true,omitempty"`
	False string `json:"false,omitempty"`
}

// ChoiceCriteria maps option keys to their descriptions. Keys come back as the
// chosen answer; the JSON object needs at least MinChoiceOptions and at most
// MaxChoiceOptions entries.
type ChoiceCriteria map[string]string

// ScoreCriteria is an ordered scale. The element at index i describes the score
// value i, so a three-element slice describes scores 0, 1 and 2.
type ScoreCriteria []string

// Question is a single named question. Build one with NewNoulQuestion,
// NewChoiceQuestion, NewScoreQuestion or ParseQuestion, all of which validate
// the payload locally so mistakes surface before the round trip.
//
// Criteria are optional for noul questions and mandatory for choice and score
// questions.
type Question struct {
	Type         QuestionType    `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

// NewNoulQuestion builds a yes/no question. The true and false descriptions
// are optional; a zero NoulCriteria leaves the decision entirely to the
// instructions, which suits simple predicates and keeps the prompt short.
func NewNoulQuestion(instructions string, criteria NoulCriteria) (Question, error) {
	if err := validateInstructions(instructions); err != nil {
		return Question{}, err
	}
	if strings.TrimSpace(criteria.True) == "" && strings.TrimSpace(criteria.False) == "" {
		// Nothing to describe, so omit the key rather than send an empty
		// object or a null, which the server treats differently.
		return Question{Type: QuestionNoul, Instructions: instructions}, nil
	}
	return newQuestion(QuestionNoul, instructions, criteria)
}

// NewChoiceQuestion builds a multiple-choice question over 2 to 26 options.
func NewChoiceQuestion(instructions string, criteria ChoiceCriteria) (Question, error) {
	if err := validateInstructions(instructions); err != nil {
		return Question{}, err
	}
	if len(criteria) < MinChoiceOptions || len(criteria) > MaxChoiceOptions {
		return Question{}, fmt.Errorf("%w: a choice question needs %d to %d options, got %d",
			ErrInvalidRequest, MinChoiceOptions, MaxChoiceOptions, len(criteria))
	}
	for key, description := range criteria {
		if strings.TrimSpace(key) == "" {
			return Question{}, fmt.Errorf("%w: a choice option has an empty key", ErrInvalidRequest)
		}
		if strings.TrimSpace(description) == "" {
			return Question{}, fmt.Errorf("%w: choice option %q has an empty description", ErrInvalidRequest, key)
		}
	}
	return newQuestion(QuestionChoice, instructions, criteria)
}

// NewScoreQuestion builds a rating question over an ordered scale. Pass one
// description per score value; the slice must not be empty.
func NewScoreQuestion(instructions string, criteria ScoreCriteria) (Question, error) {
	if err := validateInstructions(instructions); err != nil {
		return Question{}, err
	}
	if len(criteria) == 0 {
		return Question{}, fmt.Errorf("%w: a score question needs at least one description", ErrInvalidRequest)
	}
	for i, description := range criteria {
		if strings.TrimSpace(description) == "" {
			return Question{}, fmt.Errorf("%w: score %d has an empty description", ErrInvalidRequest, i)
		}
	}
	return newQuestion(QuestionScore, instructions, criteria)
}

func newQuestion(typ QuestionType, instructions string, criteria any) (Question, error) {
	raw, err := json.Marshal(criteria)
	if err != nil {
		return Question{}, fmt.Errorf("ollama: encoding %s criteria: %w", typ, err)
	}
	return Question{Type: typ, Instructions: instructions, Criteria: raw}, nil
}

func validateInstructions(instructions string) error {
	if strings.TrimSpace(instructions) == "" {
		return fmt.Errorf("%w: a question needs non-empty instructions", ErrInvalidRequest)
	}
	return nil
}

// ParseQuestion validates a question given as raw JSON, in the shape the API
// documents. It is the entry point for questions loaded from a file or config.
//
//	{"type":"noul","instructions":"...","criteria":{"true":"...","false":"..."}}
func ParseQuestion(raw json.RawMessage) (Question, error) {
	var probe struct {
		Type         QuestionType    `json:"type"`
		Instructions string          `json:"instructions"`
		Criteria     json.RawMessage `json:"criteria"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Question{}, fmt.Errorf("%w: malformed question: %w", ErrInvalidRequest, err)
	}

	switch probe.Type {
	case QuestionNoul:
		// Criteria are optional here, so an absent key decodes to the zero
		// value rather than failing.
		var criteria NoulCriteria
		if err := decodeCriteria(probe.Criteria, &criteria); err != nil {
			return Question{}, fmt.Errorf("%w: noul criteria must be an object of true/false descriptions: %w", ErrInvalidRequest, err)
		}
		return NewNoulQuestion(probe.Instructions, criteria)
	case QuestionChoice:
		criteria := ChoiceCriteria{}
		if err := decodeCriteria(probe.Criteria, &criteria); err != nil {
			return Question{}, fmt.Errorf("%w: choice criteria must map option keys to descriptions: %w", ErrInvalidRequest, err)
		}
		return NewChoiceQuestion(probe.Instructions, criteria)
	case QuestionScore:
		var criteria ScoreCriteria
		if err := decodeCriteria(probe.Criteria, &criteria); err != nil {
			return Question{}, fmt.Errorf("%w: score criteria must be an array of descriptions: %w", ErrInvalidRequest, err)
		}
		return NewScoreQuestion(probe.Instructions, criteria)
	case "":
		return Question{}, fmt.Errorf("%w: a question needs a type (%s, %s or %s)",
			ErrInvalidRequest, QuestionNoul, QuestionChoice, QuestionScore)
	default:
		return Question{}, fmt.Errorf("%w: unknown question type %q, want %s, %s or %s",
			ErrInvalidRequest, probe.Type, QuestionNoul, QuestionChoice, QuestionScore)
	}
}

// ParseQuestions validates a whole set of questions at once, given the value of
// the API's "questions" field. Every question is checked with ParseQuestion and
// the set must hold between MinQuestions and MaxQuestions entries.
//
// It exists so that a caller loading questions from a file or configuration does
// not have to reimplement the decoding and the per-question error handling that
// the CLI needs anyway.
//
//	{"saludo": {"type": "noul", "instructions": "..."}}
func ParseQuestions(raw []byte) (map[string]Question, error) {
	var rawQuestions map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawQuestions); err != nil {
		return nil, fmt.Errorf("%w: questions must be a JSON object keyed by question name: %w", ErrInvalidRequest, err)
	}
	return buildQuestions(rawQuestions)
}

// LoadQuestions is ParseQuestions reading from an io.Reader.
func LoadQuestions(r io.Reader) (map[string]Question, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%w: reading questions: %w", ErrInvalidRequest, err)
	}
	return ParseQuestions(raw)
}

// buildQuestions validates an already-decoded set, reporting the first problem
// against the name of the offending question.
func buildQuestions(rawQuestions map[string]json.RawMessage) (map[string]Question, error) {
	if len(rawQuestions) < MinQuestions || len(rawQuestions) > MaxQuestions {
		return nil, fmt.Errorf("%w: questions must contain %d to %d fields, got %d",
			ErrInvalidRequest, MinQuestions, MaxQuestions, len(rawQuestions))
	}

	questions := make(map[string]Question, len(rawQuestions))
	// Sorted so a malformed set always fails on the same question, which
	// keeps the error reproducible.
	names := make([]string, 0, len(rawQuestions))
	for name := range rawQuestions {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		question, err := ParseQuestion(rawQuestions[name])
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", name, err)
		}
		questions[name] = question
	}
	return questions, nil
}

// DecideYesNo evaluates a single yes/no question against the state and returns
// its answer. It is the common case, so it is wrapped rather than left as a
// hand-built Questions map.
func (c *Client) DecideYesNo(ctx context.Context, model string, state State, question Question) (Answer, error) {
	if question.Type != QuestionNoul {
		return Answer{}, fmt.Errorf("%w: DecideYesNo needs a %s question, got %q",
			ErrInvalidRequest, QuestionNoul, question.Type)
	}

	resp, err := c.Decide(ctx, DecideRequest{
		Model:     model,
		State:     state,
		Questions: map[string]Question{"q": question},
	})
	if err != nil {
		return Answer{}, err
	}
	return resp.Answers["q"], nil
}

// decodeCriteria decodes a question's criteria into out, tolerating an absent
// key or an explicit null, which the client normalises to "no criteria" so the
// key is omitted on the way out.
func decodeCriteria(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Options carries the generation knobs the server honours.
type Options struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	Seed        *int     `json:"seed,omitempty"`
	NumPredict  *int     `json:"num_predict,omitempty"`
}

// State is the input the model judges. It accepts either a plain string or
// structured data: a decoded JSON object, a slice, or raw bytes. The server
// serialises it as-is, so a map[string]any reaches the model as a JSON object
// rather than as Go's formatting of a struct.
type State any

// StringState judges plain text.
func StringState(text string) State { return text }

// JSONState judges JSON you encoded yourself, for instance a document read from
// disk that should reach the model byte for byte.
func JSONState(raw []byte) State { return json.RawMessage(raw) }

// DecideRequest asks the model to evaluate State against Questions. The
// questions map may hold between MinQuestions and MaxQuestions entries, and the
// server answers every one of them in a single call.
type DecideRequest struct {
	Model string `json:"model"`
	// State is the text or structured data to judge.
	State     State               `json:"state"`
	Questions map[string]Question `json:"questions"`
	// System optionally overrides the classifier's system prompt.
	System string `json:"system,omitempty"`
	// KeepAlive controls how long the model stays loaded, e.g. "5m".
	KeepAlive string `json:"keep_alive,omitempty"`
	// Options tunes sampling; leave nil to use the server defaults, which are
	// already tuned for deterministic decisions.
	Options *Options `json:"options,omitempty"`
}

// Validate checks the request against the server's documented constraints.
func (r DecideRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidRequest)
	}
	if r.State == nil {
		return fmt.Errorf("%w: state is required", ErrInvalidRequest)
	}
	if len(r.Questions) < MinQuestions || len(r.Questions) > MaxQuestions {
		return fmt.Errorf("%w: questions must contain %d to %d fields, got %d",
			ErrInvalidRequest, MinQuestions, MaxQuestions, len(r.Questions))
	}
	for name, question := range r.Questions {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: a question name must not be empty", ErrInvalidRequest)
		}
		if err := validateInstructions(question.Instructions); err != nil {
			return fmt.Errorf("question %q: %w", name, err)
		}
		switch question.Type {
		case QuestionNoul, QuestionChoice, QuestionScore:
		default:
			return fmt.Errorf("%w: question %q has unknown type %q, want %s, %s or %s",
				ErrInvalidRequest, name, question.Type, QuestionNoul, QuestionChoice, QuestionScore)
		}
		// A noul question may omit its criteria and lean on the
		// instructions; choice and score always need them.
		if question.Type != QuestionNoul && len(question.Criteria) == 0 {
			return fmt.Errorf("%w: question %q of type %s needs criteria",
				ErrInvalidRequest, name, question.Type)
		}
	}
	return nil
}

// Usage reports the token accounting for one call.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// DecideResponse is the result of a Decide call.
//
// Answers come back keyed by question name. Because the server returns them in
// the same order the questions were sent, Order preserves that sequence;
// AnswersInOrder replays it.
type DecideResponse struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	// Order lists the question names as the server returned them.
	Order []string `json:"-"`
	Usage Usage    `json:"usage"`
	// Duration is the wall-clock time Decide spent on the round trip. It is
	// measured by the client, not reported by the server, so it stays out of
	// the JSON shape.
	Duration time.Duration `json:"-"`
}

// NamedAnswer pairs a question name with its answer.
type NamedAnswer struct {
	Name   string
	Answer Answer
}

// UnmarshalJSON decodes the response and keeps the server's answer order,
// which a plain map decode would discard.
func (r *DecideResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		Model   string            `json:"model"`
		Answers map[string]Answer `json:"answers"`
		Usage   Usage             `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	r.Model = raw.Model
	r.Answers = raw.Answers
	r.Usage = raw.Usage
	r.Order = answerOrder(data)
	return nil
}

// answerOrder recovers the key order of the "answers" object by walking the
// JSON stream, since Go maps are unordered.
func answerOrder(data []byte) []string {
	var envelope struct {
		Answers json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Answers) == 0 {
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(envelope.Answers))
	if _, err := decoder.Token(); err != nil { // the opening brace
		return nil
	}

	var order []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return order
		}
		name, ok := key.(string)
		if !ok {
			return order
		}
		order = append(order, name)

		var discard json.RawMessage
		if err := decoder.Decode(&discard); err != nil { // skip the answer
			return order
		}
	}
	return order
}

// AnswersInOrder returns the answers in the order the server sent them, which
// matches the order the questions were sent in. Names missing from the response
// are skipped, and a response that carried no usable order falls back to
// alphabetical order so callers still get a deterministic listing.
func (r *DecideResponse) AnswersInOrder() []NamedAnswer {
	names := r.Order
	if len(names) == 0 {
		names = make([]string, 0, len(r.Answers))
		for name := range r.Answers {
			names = append(names, name)
		}
		sort.Strings(names)
	}

	ordered := make([]NamedAnswer, 0, len(r.Answers))
	for _, name := range names {
		if answer, ok := r.Answers[name]; ok {
			ordered = append(ordered, NamedAnswer{Name: name, Answer: answer})
		}
	}
	return ordered
}

// Answer is one question's result. Only the fields matching Type are set; use
// AsNoul, AsChoice and AsScore to read them with the right type.
type Answer struct {
	Type QuestionType
	// Noul is the probability that the noul question's true criteria holds,
	// between 0 and 1.
	Noul float64
	// Choice is the key selected for a choice question.
	Choice string
	// Score is the position picked on a score question's scale.
	Score float64
	// Legend maps a score index to its description, echoing the criteria.
	Legend map[string]string
	// Probabilities maps a choice option key, or a score index as a decimal
	// string, to its probability.
	Probabilities map[string]float64
	// Confidence is reported for choice and score questions; noul questions
	// carry their confidence in Noul instead. It runs from 0 to 1 and measures
	// how concentrated the probabilities are. It is not the probability that
	// the answer is correct.
	Confidence float64
}

// NoulAnswer is the typed view of a yes/no answer.
type NoulAnswer struct {
	// Probability that the true criteria holds.
	Probability float64
}

// Verdict applies a threshold to the probability. The zero value defaults to
// 0.5.
func (n NoulAnswer) Verdict(threshold float64) bool {
	if threshold <= 0 {
		threshold = 0.5
	}
	return n.Probability >= threshold
}

// ChoiceAnswer is the typed view of a multiple-choice answer.
type ChoiceAnswer struct {
	Selected      string
	Probabilities map[string]float64
	Confidence    float64
}

// ScoreAnswer is the typed view of a rating answer.
type ScoreAnswer struct {
	Value         float64
	Legend        map[string]string
	Probabilities map[string]float64
	Confidence    float64
}

// answerWire mirrors the server's union shape. Pointers tell the concrete type
// apart from a legitimately zero value.
type answerWire struct {
	Type          QuestionType       `json:"type"`
	Noul          *float64           `json:"noul"`
	Choice        *string            `json:"choice"`
	Score         *float64           `json:"score"`
	Legend        map[string]string  `json:"legend"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    *float64           `json:"confidence"`
}

// UnmarshalJSON decodes whichever concrete answer the "type" field selects.
func (a *Answer) UnmarshalJSON(data []byte) error {
	var wire answerWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	a.Type = wire.Type
	a.Legend = wire.Legend
	a.Probabilities = wire.Probabilities
	if wire.Confidence != nil {
		a.Confidence = *wire.Confidence
	}

	switch wire.Type {
	case QuestionNoul:
		if wire.Noul != nil {
			a.Noul = *wire.Noul
		}
	case QuestionChoice:
		if wire.Choice != nil {
			a.Choice = *wire.Choice
		}
	case QuestionScore:
		if wire.Score != nil {
			a.Score = *wire.Score
		}
	}
	return nil
}

// MarshalJSON re-encodes the answer in the server's own shape, mirroring
// UnmarshalJSON, so a decoded response round-trips through the API format
// instead of exposing Go field names.
func (a Answer) MarshalJSON() ([]byte, error) {
	wire := struct {
		Type          QuestionType       `json:"type"`
		Noul          *float64           `json:"noul,omitempty"`
		Choice        *string            `json:"choice,omitempty"`
		Score         *float64           `json:"score,omitempty"`
		Legend        map[string]string  `json:"legend,omitempty"`
		Probabilities map[string]float64 `json:"probabilities,omitempty"`
		Confidence    *float64           `json:"confidence,omitempty"`
	}{Type: a.Type}

	// Pointers, not plain values: a probability or score of exactly zero is
	// meaningful and must not be dropped.
	switch a.Type {
	case QuestionNoul:
		probability := a.Noul
		wire.Noul = &probability
	case QuestionChoice:
		if a.Choice != "" {
			selected := a.Choice
			wire.Choice = &selected
		}
		if a.Confidence != 0 {
			confidence := a.Confidence
			wire.Confidence = &confidence
		}
		wire.Probabilities = a.Probabilities
	case QuestionScore:
		score := a.Score
		wire.Score = &score
		if a.Confidence != 0 {
			confidence := a.Confidence
			wire.Confidence = &confidence
		}
		wire.Legend = a.Legend
		wire.Probabilities = a.Probabilities
	}
	return json.Marshal(wire)
}

// AsNoul returns the yes/no view, reporting false for other types.
func (a Answer) AsNoul() (NoulAnswer, bool) {
	if a.Type != QuestionNoul {
		return NoulAnswer{}, false
	}
	return NoulAnswer{Probability: a.Noul}, true
}

// AsChoice returns the multiple-choice view, reporting false for other types.
func (a Answer) AsChoice() (ChoiceAnswer, bool) {
	if a.Type != QuestionChoice {
		return ChoiceAnswer{}, false
	}
	return ChoiceAnswer{
		Selected:      a.Choice,
		Probabilities: a.Probabilities,
		Confidence:    a.Confidence,
	}, true
}

// AsScore returns the rating view, reporting false for other types.
func (a Answer) AsScore() (ScoreAnswer, bool) {
	if a.Type != QuestionScore {
		return ScoreAnswer{}, false
	}
	return ScoreAnswer{
		Value:         a.Score,
		Legend:        a.Legend,
		Probabilities: a.Probabilities,
		Confidence:    a.Confidence,
	}, true
}

// Top returns the option with the highest probability, breaking ties by
// lexicographic key so the result is stable.
func (c ChoiceAnswer) Top() (string, float64) {
	keys := make([]string, 0, len(c.Probabilities))
	for key := range c.Probabilities {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	best, bestProbability := "", 0.0
	for i, key := range keys {
		if i == 0 || c.Probabilities[key] > bestProbability {
			best, bestProbability = key, c.Probabilities[key]
		}
	}
	return best, bestProbability
}

// Bucket returns the score index with the highest probability, plus its legend
// label when the server provided one. The second result is false if the
// probabilities are missing or unparseable.
func (s ScoreAnswer) Bucket() (index int, label string, ok bool) {
	if len(s.Probabilities) == 0 {
		return 0, "", false
	}

	keys := make([]string, 0, len(s.Probabilities))
	for key := range s.Probabilities {
		keys = append(keys, key)
	}
	// Sort numerically so ties resolve to the lowest index.
	sort.Slice(keys, func(i, j int) bool {
		a, errA := strconv.Atoi(keys[i])
		b, errB := strconv.Atoi(keys[j])
		if errA != nil || errB != nil {
			return keys[i] < keys[j]
		}
		return a < b
	})

	best, bestProbability := 0, 0.0
	for i, key := range keys {
		if i == 0 || s.Probabilities[key] > bestProbability {
			parsed, err := strconv.Atoi(key)
			if err != nil {
				return 0, "", false
			}
			best, bestProbability = parsed, s.Probabilities[key]
		}
	}
	return best, s.Legend[strconv.Itoa(best)], true
}

// EstimatePromptTokens returns a rough upper bound on the largest single
// question prompt: the state, plus that question's instructions and criteria.
// The server builds one prompt per question and scores them independently, so
// the question count does not multiply this figure; what matters is that the
// state leaves room for the longest question.
//
// The value is approximate. See ApproxTokens.
func (r DecideRequest) EstimatePromptTokens() int {
	stateJSON, err := json.Marshal(r.State)
	if err != nil {
		return 0
	}
	stateTokens := ApproxTokens(string(stateJSON))

	longest := 0
	for _, question := range r.Questions {
		questionTokens := stateTokens +
			ApproxTokens(question.Instructions) +
			ApproxTokens(string(question.Criteria))
		if questionTokens > longest {
			longest = questionTokens
		}
	}
	return longest
}

// Decide evaluates the state against every question in one round trip. It
// validates the request first, so constraint violations come back as errors
// wrapping ErrInvalidRequest instead of an HTTP 400.
func (c *Client) Decide(ctx context.Context, req DecideRequest) (*DecideResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: encoding request body: %w", err)
	}
	// Checked here so an oversized state or instruction set fails with a
	// usable message instead of a bare HTTP 413.
	if len(payload) > MaxRequestBytes {
		return nil, fmt.Errorf("%w: request body is %d bytes but the server accepts at most %d (64 KiB); "+
			"shorten the state or the instructions", ErrInvalidRequest, len(payload), MaxRequestBytes)
	}

	var resp DecideResponse
	// Passing the raw bytes avoids marshalling the request twice.
	start := time.Now()
	if err := c.postJSON(ctx, "/v1/systemone", json.RawMessage(payload), &resp); err != nil {
		return nil, err
	}
	resp.Duration = time.Since(start)
	return &resp, nil
}
