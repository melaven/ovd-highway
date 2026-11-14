package lane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleClientComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1beta/openai/chat/completions" {
			t.Errorf("path = %q, want /v1beta/openai/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}

		var request openAIRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Model != "test-model" || len(request.Messages) != 2 ||
			request.Messages[0].Role != "system" || request.Messages[0].Content != "system prompt" ||
			request.Messages[1].Role != "user" || request.Messages[1].Content != "user prompt" {
			t.Errorf("unexpected request: %+v", request)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"result"}}]}`))
	}))
	defer server.Close()

	client, err := NewOpenAICompatibleClient("test-key", server.URL+"/v1beta/openai/", "test-model")
	if err != nil {
		t.Fatalf("NewOpenAICompatibleClient: %v", err)
	}

	got, err := client.Complete(context.Background(), "system prompt", "user prompt")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "result" {
		t.Fatalf("Complete = %q, want result", got)
	}
}

func TestOpenAICompatibleClientStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := NewOpenAICompatibleClient("test-key", server.URL, "test-model")
	if err != nil {
		t.Fatalf("NewOpenAICompatibleClient: %v", err)
	}

	_, err = client.Complete(context.Background(), "system", "user")
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.Code != http.StatusServiceUnavailable {
		t.Fatalf("Complete error = %v, want status error %d", err, http.StatusServiceUnavailable)
	}
}

func TestNewOpenAICompatibleClientRejectsInvalidBaseURL(t *testing.T) {
	_, err := NewOpenAICompatibleClient("test-key", "file:///tmp/llm", "test-model")
	if err == nil {
		t.Fatal("NewOpenAICompatibleClient accepted a non-http URL")
	}
}
