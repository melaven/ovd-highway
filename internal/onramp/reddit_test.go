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
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if got := r.Header.Get("User-Agent"); got != redditUserAgent {
			t.Errorf("User-Agent = %q, want %q", got, redditUserAgent)
		}
		_, _ = w.Write([]byte(`{"data":{"children":[
			{"data":{"title":"[Hiring] Go engineer","selftext":"Build an API","permalink":"/r/forhire/comments/abc/post","created_utc":1794912000}},
			{"data":{"title":"[For Hire] Go engineer","selftext":"Seeking work","permalink":"/r/forhire/comments/def/post","created_utc":1794912000}}
		]}}`))
	}))
	defer server.Close()

	connector := NewRedditConnector()
	connector.client = server.Client()
	connector.endpoint = server.URL

	signals := make(chan domain.RawSignal, 2)
	if err := connector.poll(context.Background(), signals); err != nil {
		t.Fatalf("poll: %v", err)
	}
	close(signals)

	var got []domain.RawSignal
	for signal := range signals {
		got = append(got, signal)
	}
	if len(got) != 1 {
		t.Fatalf("signals = %d, want 1", len(got))
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
