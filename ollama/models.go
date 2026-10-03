package ollama

import (
	"context"
)

// Model describes one entry of the OpenAI-compatible model list.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ListModels returns the models the server advertises. Use the returned ID as
// the Model field of a DecideRequest.
func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	var payload struct {
		Data []Model `json:"data"`
	}
	if err := c.getJSON(ctx, "/v1/models", &payload); err != nil {
		return nil, err
	}
	return payload.Data, nil
}
