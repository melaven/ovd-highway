package lane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type Completer interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("llm http status %d: %s", e.Code, e.Body)
}

type AnthropicClient struct {
	http   *http.Client
	apiKey string
	model  string
}

func NewAnthropicFromEnv() (*AnthropicClient, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	model := os.Getenv("ANTHROPIC_MODEL")
	if key == "" || model == "" {
		return nil, errors.New("ANTHROPIC_API_KEY and ANTHROPIC_MODEL must be set")
	}
	return &AnthropicClient{
		http:   &http.Client{Timeout: 60 * time.Second},
		apiKey: key,
		model:  model,
	}, nil
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (c *AnthropicClient) Complete(ctx context.Context, system, user string) (string, error) {
	payload, err := json.Marshal(anthropicRequest{
		Model:     c.model,
		MaxTokens: 1500,
		System:    system,
		Messages:  []anthropicMessage{{Role: "user", Content: user}},
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", &StatusError{Code: resp.StatusCode, Body: string(body)}
	}

	var out anthropicResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	for _, b := range out.Content {
		if b.Type == "text" {
			return b.Text, nil
		}
	}
	return "", errors.New("llm response has no text block")
}
