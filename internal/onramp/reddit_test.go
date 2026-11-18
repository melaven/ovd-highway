package onramp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"ovd/internal/domain"
)

func TestRedditConnectorPollsHiringPosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/access_token":
			if r.Method != http.MethodPost {
				t.Errorf("token method = %q, want POST", r.Method)
			}
			clientID, clientSecret, ok := r.BasicAuth()
			if !ok || clientID != "test-client-id" || clientSecret != "test-client-secret" {
				t.Errorf("token basic auth = %q/%q ok=%v", clientID, clientSecret, ok)
			}
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" {
				t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
			}
			_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"bearer","expires_in":3600}`))
		default:
			if r.Method != http.MethodGet {
				t.Errorf("method = %q, want GET", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
			}
			wantUA := "windows:ovd-highway:v1.0.0 (by /u/melaven)"
			if got := r.Header.Get("User-Agent"); got != wantUA {
				t.Errorf("User-Agent = %q, want %q", got, wantUA)
			}
			_, _ = w.Write([]byte(`{"data":{"children":[
				{"data":{"title":"[Hiring] Go engineer","selftext":"Build an API","permalink":"/r/forhire/comments/abc/post","created_utc":1794912000}},
				{"data":{"title":"[hiring] lowercase tag","selftext":"Build a bot","permalink":"/r/forhire/comments/ghi/post","created_utc":1794912000}},
				{"data":{"title":"[For Hire] Go engineer","selftext":"Seeking work","permalink":"/r/forhire/comments/def/post","created_utc":1794912000}}
			]}}`))
		}
	}))
	defer server.Close()

	connector := NewRedditConnector()
	connector.client = server.Client()
	connector.tokens.client = server.Client()
	connector.tokens.tokenURL = server.URL + "/api/v1/access_token"
	connector.tokens.clientID = "test-client-id"
	connector.tokens.clientSecret = "test-client-secret"
	connector.tokens.user = "melaven"
	connector.endpoint = server.URL + "/r/forhire/new.json"

	signals := make(chan domain.RawSignal, 3)
	if err := connector.poll(context.Background(), signals); err != nil {
		t.Fatalf("poll: %v", err)
	}
	close(signals)

	var got []domain.RawSignal
	for signal := range signals {
		got = append(got, signal)
	}
	if len(got) != 2 {
		t.Fatalf("signals = %d, want 2", len(got))
	}
	if got[0].Source != "reddit" || got[0].JobType != "freelance" {
		t.Errorf("signal metadata = %+v", got[0])
	}
	if got[0].URL != "https://www.reddit.com/r/forhire/comments/abc/post" {
		t.Errorf("signal URL = %q", got[0].URL)
	}
	if got[0].Text != "[Hiring] Go engineer\nBuild an API" {
		t.Errorf("signal text = %q", got[0].Text)
	}
}
