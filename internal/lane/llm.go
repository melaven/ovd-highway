package lane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

type OpenAICompatibleClient struct {
	http     *http.Client
	apiKey   string
	model    string
	endpoint string
}

func NewOpenAICompatibleClient(apiKey, baseURL, model string) (*OpenAICompatibleClient, error) {
	if apiKey == "" || baseURL == "" || model == "" {
		return nil, errors.New("LLM_API_KEY, LLM_BASE_URL, and LLM_MODEL must be set")
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse LLM_BASE_URL: %w", err)
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" ||
		base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("LLM_BASE_URL must be an http(s) URL without user info, query, or fragment")
	}

	endpoint := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	return &OpenAICompatibleClient{
		http:     &http.Client{Timeout: 60 * time.Second},
		apiKey:   apiKey,
		model:    model,
		endpoint: endpoint,
	}, nil
}

type openAIRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []openAIMessage `json:"messages"`
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

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", &StatusError{Code: resp.StatusCode, Body: string(body)}
	}

	var out openAIResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("llm response has no message content")
	}
	return out.Choices[0].Message.Content, nil
}
