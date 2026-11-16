package lane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
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

// DefaultModel is a free, JSON-reliable model on OpenRouter.
const DefaultModel = "meta-llama/llama-3.3-8b-instruct:free"

// OpenRouterBaseURL is the OpenAI-compatible endpoint of OpenRouter.
const OpenRouterBaseURL = "https://openrouter.ai/api/v1"

type OpenAICompatibleClient struct {
	http     *http.Client
	apiKey   string
	model    string
	endpoint string
	headers  map[string]string
}

func NewOpenAICompatibleClient(apiKey, baseURL, model string) (*OpenAICompatibleClient, error) {
	return newChatClient(apiKey, baseURL, model, nil)
}

// NewOpenRouterClient builds a client for OpenRouter with default headers.
func NewOpenRouterClient(apiKey, model string) (*OpenAICompatibleClient, error) {
	if model == "" {
		model = DefaultModel
	}
	c, err := newChatClient(apiKey, OpenRouterBaseURL, model, map[string]string{
		"HTTP-Referer": "https://github.com/ovd-highway",
		"X-Title":      "OVD Highway",
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func newChatClient(apiKey, baseURL, model string, headers map[string]string) (*OpenAICompatibleClient, error) {
	if apiKey == "" || baseURL == "" || model == "" {
		return nil, errors.New("api key, base URL, and model must be set")
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" ||
		base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("base URL must be an http(s) URL without user info, query, or fragment")
	}

	endpoint := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	return &OpenAICompatibleClient{
		http:     &http.Client{Timeout: 15 * time.Second},
		apiKey:   apiKey,
		model:    model,
		endpoint: endpoint,
		headers:  headers,
	}, nil
}

type openAIRequest struct {
	Model          string                `json:"model"`
	MaxTokens      int                   `json:"max_tokens"`
	Messages       []openAIMessage       `json:"messages"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
}

type openAIResponseFormat struct {
	Type string `json:"type"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *OpenAICompatibleClient) Complete(ctx context.Context, system, user string) (string, error) {
	payload, err := json.Marshal(openAIRequest{
		Model:     c.model,
		MaxTokens: 1500,
		ResponseFormat: &openAIResponseFormat{
			Type: "json_object",
		},
		Messages: []openAIMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+c.apiKey)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	slog.Info("LLM request started")
	resp, err := c.http.Do(req)
	if err != nil {
		slog.Error("LLM request failed", "err", err)
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	slog.Info("LLM response received", "status", resp.StatusCode)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		slog.Error("LLM response read failed", "err", err)
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		slog.Error("LLM returned error status", "status", resp.StatusCode, "body", string(body))
		return "", &StatusError{Code: resp.StatusCode, Body: string(body)}
	}

	var out openAIResponse
	if err := json.Unmarshal(body, &out); err != nil {
		slog.Error("LLM response decode failed", "err", err)
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		slog.Error("LLM response has no message content")
		return "", errors.New("llm response has no message content")
	}
	return out.Choices[0].Message.Content, nil
}
