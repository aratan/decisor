package ollama

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Role values accepted by the chat API.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// ReasoningEffort caps how long a thinking model deliberates before it answers.
// Only ReasoningNone actually skips the reasoning step; the rest merely bound
// it, and on a given model every level but "none" may still think.
type ReasoningEffort string

// The levels the server accepts.
const (
	ReasoningNone      ReasoningEffort = "none"
	ReasoningMinimal   ReasoningEffort = "minimal"
	ReasoningLow       ReasoningEffort = "low"
	ReasoningMedium    ReasoningEffort = "medium"
	ReasoningHigh      ReasoningEffort = "high"
	ReasoningExtraHigh ReasoningEffort = "xhigh"
	ReasoningUltra     ReasoningEffort = "ultra"
	ReasoningMax       ReasoningEffort = "max"
)

// Valid reports whether e is a level the server accepts.
func (e ReasoningEffort) Valid() bool {
	switch e {
	case ReasoningNone, ReasoningMinimal, ReasoningLow, ReasoningMedium,
		ReasoningHigh, ReasoningExtraHigh, ReasoningUltra, ReasoningMax:
		return true
	default:
		return false
	}
}

// String makes ReasoningEffort printable in error messages.
func (e ReasoningEffort) String() string { return string(e) }

// Message is one entry in a chat conversation.
type Message struct {
	Role string `json:"role"`
	// Content is the visible text of the turn.
	Content string `json:"content"`
	// Reasoning holds the chain of thought on models that expose one, such as
	// those with the "thinking" capability. Ollama returns it as a separate
	// field rather than inside Content.
	Reasoning string `json:"reasoning,omitempty"`
	// Name optionally labels the speaker.
	Name string `json:"name,omitempty"`
}

// Text builds a user message.
func Text(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

// ChatRequest is the body of a /v1/chat/completions call.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	// Temperature, TopP, Seed and MaxTokens are omitted when nil, leaving the
	// server defaults in place.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Seed        *int     `json:"seed,omitempty"`
	// Stop holds up to four sequences that end generation.
	Stop []string `json:"stop,omitempty"`
	// KeepAlive controls how long the model stays loaded, e.g. "5m".
	KeepAlive string `json:"keep_alive,omitempty"`
	// ResponseFormat selects the output shape; "json" asks for JSON output.
	ResponseFormat string `json:"response_format,omitempty"`
	// ReasoningEffort bounds the deliberation of a thinking model. Setting
	// ReasoningNone skips the reasoning step entirely, which on a Qwen-based
	// model can be the difference between a two-second reply and one that
	// burns the whole token budget on thinking.
	//
	// Note that the native Ollama "think" field is not honoured by the
	// OpenAI-compatible endpoint; this one is.
	ReasoningEffort ReasoningEffort `json:"reasoning_effort,omitempty"`

	// Stream asks for incremental delivery over server-sent events. Chat
	// forces it off; ChatStream sets it. Leave it unset unless you are
	// driving the event loop yourself.
	Stream bool `json:"stream,omitempty"`
}

// ChatUsage is the token accounting of a chat completion.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatChoice is a single completion candidate.
type ChatChoice struct {
	Index int `json:"index"`
	// Message is the assistant turn produced. On streamed responses this
	// holds the incremental delta instead.
	Message Message `json:"message"`
	// FinishReason is why generation stopped, e.g. "stop" or "length".
	FinishReason string `json:"finish_reason"`
}

// ChatResponse is a complete, non-streamed completion.
type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
	// Duration is the wall-clock time Chat spent on the round trip, measured
	// by the client rather than reported by the server.
	Duration time.Duration `json:"-"`
}

// Text returns the first choice's content, or "" when there are no choices.
func (r *ChatResponse) Text() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}

// Reasoning returns the first choice's reasoning trace, if the model emits one.
func (r *ChatResponse) Reasoning() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Reasoning
}

// Chat completes a conversation in one shot.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	var resp ChatResponse
	req.Stream = false
	start := time.Now()
	if err := c.postJSON(ctx, "/v1/chat/completions", req, &resp); err != nil {
		return nil, err
	}
	resp.Duration = time.Since(start)
	return &resp, nil
}

// ChatChunk is one server-sent event from a streamed completion.
type ChatChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []ChatChunkChoice `json:"choices"`
}

// ChatChunkChoice carries the incremental text of a streamed completion.
type ChatChunkChoice struct {
	Index int `json:"index"`
	// Delta holds the newly generated fragment. Both the visible content and
	// the reasoning trace arrive here.
	Delta Message `json:"delta"`
	// FinishReason is set on the final chunk of a choice.
	FinishReason string `json:"finish_reason"`
}

// Text returns the chunk's visible content.
func (ch ChatChunk) Text() string {
	var b strings.Builder
	for _, choice := range ch.Choices {
		b.WriteString(choice.Delta.Content)
	}
	return b.String()
}

// Reasoning returns the chunk's reasoning fragment.
func (ch ChatChunk) Reasoning() string {
	var b strings.Builder
	for _, choice := range ch.Choices {
		b.WriteString(choice.Delta.Reasoning)
	}
	return b.String()
}

// FinishReason returns why generation stopped: "stop" for a natural ending,
// "length" when the token budget ran out, or "" while still going. On a
// thinking model, "length" often means the entire budget went to the
// reasoning trace and no visible text was produced.
func (ch ChatChunk) FinishReason() string {
	for _, choice := range ch.Choices {
		if choice.FinishReason != "" {
			return choice.FinishReason
		}
	}
	return ""
}

// StreamStats measures a streamed completion, timed from the moment the request
// was sent. On a thinking model the gap between FirstToken and Duration is the
// time spent reasoning, which is often most of the wait.
type StreamStats struct {
	// Duration is the wall-clock time from sending the request to the last
	// event, or to the handler returning. It includes connecting and waiting
	// for the response headers.
	Duration time.Duration
	// FirstToken is the delay from sending the request until the first event
	// carrying any text, visible or reasoning. On a thinking model this is
	// where the deliberation shows up. It is zero if the stream carried no
	// text at all.
	FirstToken time.Duration
	// Chunks counts the events handed to the handler.
	Chunks int
	// Content and Reasoning hold the whole response, so callers that only
	// print incrementally do not have to reassemble it.
	Content   string
	Reasoning string
}

// TokensPerSecond returns the rate of visible tokens produced, or 0 when there
// was no visible output. It counts whitespace-separated pieces, which is a
// rough proxy rather than a real tokenizer count.
func (s StreamStats) TokensPerSecond() float64 {
	if s.Duration <= 0 {
		return 0
	}
	tokens := 0
	if fields := strings.Fields(s.Content); len(fields) > 0 {
		tokens = len(fields)
	}
	if tokens == 0 {
		return 0
	}
	return float64(tokens) / s.Duration.Seconds()
}

// ChatStream completes a conversation incrementally, calling fn for every
// event until generation finishes. Returning an error from fn aborts the
// stream and propagates that error.
//
// Note that a reasoning-capable model such as "nimble" emits its thinking
// trace before any visible text, so handlers should be prepared for a long
// silent stretch.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, fn func(ChatChunk) error) error {
	_, err := c.ChatStreamWithStats(ctx, req, fn)
	return err
}

// ChatStreamWithStats behaves like ChatStream and additionally reports
// timings and the reassembled response.
func (c *Client) ChatStreamWithStats(ctx context.Context, req ChatRequest, fn func(ChatChunk) error) (*StreamStats, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	if fn == nil {
		return nil, fmt.Errorf("%w: ChatStream needs a handler function", ErrInvalidRequest)
	}

	req.Stream = true
	// The clock starts before the request goes out, so Duration covers
	// connecting and waiting for headers, not just reading events. Starting it
	// after openStream would quietly under-report the real latency.
	start := time.Now()

	body, err := c.openStream(ctx, "/v1/chat/completions", req)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	stats := &StreamStats{}
	var content, reasoning strings.Builder

	scanner := bufio.NewScanner(body)
	// Reasoning traces can contain long unbroken lines.
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk ChatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Skip keep-alive frames or anything else the server may inject
			// rather than failing the whole stream.
			continue
		}

		text, thought := chunk.Text(), chunk.Reasoning()
		content.WriteString(text)
		reasoning.WriteString(thought)
		if stats.FirstToken == 0 && text+thought != "" {
			stats.FirstToken = time.Since(start)
		}
		stats.Chunks++

		if err := fn(chunk); err != nil {
			stats.Duration = time.Since(start)
			stats.Content, stats.Reasoning = content.String(), reasoning.String()
			return stats, err
		}
	}

	stats.Duration = time.Since(start)
	stats.Content, stats.Reasoning = content.String(), reasoning.String()
	return stats, scanner.Err()
}

// Complete is a convenience for a single-turn question answered in one shot.
// It returns only the visible text of the reply.
func (c *Client) Complete(ctx context.Context, model, prompt string) (string, error) {
	resp, err := c.Chat(ctx, ChatRequest{
		Model:    model,
		Messages: []Message{Text(prompt)},
	})
	if err != nil {
		return "", err
	}
	return resp.Text(), nil
}

func (r ChatRequest) validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidRequest)
	}
	if r.ReasoningEffort != "" && !r.ReasoningEffort.Valid() {
		return fmt.Errorf("%w: unknown reasoning effort %q, want one of %s",
			ErrInvalidRequest, r.ReasoningEffort, reasoningLevels)
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("%w: at least one message is required", ErrInvalidRequest)
	}
	for i, message := range r.Messages {
		if strings.TrimSpace(message.Role) == "" {
			return fmt.Errorf("%w: message %d has no role", ErrInvalidRequest, i)
		}
		if strings.TrimSpace(message.Content) == "" && strings.TrimSpace(message.Reasoning) == "" {
			return fmt.Errorf("%w: message %d has empty content", ErrInvalidRequest, i)
		}
	}
	return nil
}

// reasoningLevels lists the accepted levels for error messages.
const reasoningLevels = "none, minimal, low, medium, high, xhigh, ultra, max"
