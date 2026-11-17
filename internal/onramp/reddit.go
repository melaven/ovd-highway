package onramp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"ovd/internal/domain"
)

const (
	redditPostsURL   = "https://www.reddit.com/r/forhire/new.json"
	redditUserAgent  = "ovd-highway:v1.0 (by /u/melaven)"
	redditPollPeriod = 60 * time.Second
)

type RedditConnector struct {
	client   *http.Client
	endpoint string
}

func NewRedditConnector() *RedditConnector {
	return &RedditConnector{
		client:   &http.Client{Timeout: 20 * time.Second},
		endpoint: redditPostsURL,
	}
}

func (r *RedditConnector) Name() string {
	return "reddit"
}

func (r *RedditConnector) Start(ctx context.Context, signals chan<- domain.RawSignal) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := r.poll(ctx, signals); err != nil && ctx.Err() == nil {
			log.Printf("reddit connector poll failed: %v", err)
		}

		timer := time.NewTimer(redditPollPeriod)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (r *RedditConnector) poll(ctx context.Context, signals chan<- domain.RawSignal) error {
	req, err := http.NewRequest(http.MethodGet, r.endpoint, nil)
	if err != nil {
		return fmt.Errorf("create Reddit request: %w", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("User-Agent", redditUserAgent)

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("request Reddit posts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Reddit returned HTTP status %d", resp.StatusCode)
	}

	var listing redditListing
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&listing); err != nil {
		return fmt.Errorf("decode Reddit posts: %w", err)
	}

	for _, child := range listing.Data.Children {
		post := child.Data
		if !strings.Contains(post.Title, "[Hiring]") {
			continue
		}

		receivedAt := time.Now().UTC()
		if post.CreatedUTC > 0 {
			receivedAt = time.Unix(int64(post.CreatedUTC), 0).UTC()
		}
		postURL := post.Permalink
		if strings.HasPrefix(postURL, "/") {
			postURL = "https://www.reddit.com" + postURL
		}

		signal := domain.RawSignal{
			ID:         uuid.NewString(),
			Source:     "reddit",
			URL:        postURL,
			Text:       strings.TrimSpace(post.Title + "\n" + post.SelfText),
			ReceivedAt: receivedAt,
			JobType:    "freelance",
		}
		select {
		case signals <- signal:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

type redditListing struct {
	Data struct {
		Children []struct {
			Data redditPost `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

type redditPost struct {
	Title      string  `json:"title"`
	SelfText   string  `json:"selftext"`
	Permalink  string  `json:"permalink"`
	CreatedUTC float64 `json:"created_utc"`
}
