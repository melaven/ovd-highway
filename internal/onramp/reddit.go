package onramp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"ovd/internal/domain"
	"ovd/internal/pkg/breaker"
)

const (
	redditPostsURL   = "https://oauth.reddit.com/r/forhire/new.json"
	redditTokenURL   = "https://www.reddit.com/api/v1/access_token"
	redditUserAgent  = "windows:ovd-highway:v1.0.0 (by /u/melaven)"
	redditPollPeriod = 60 * time.Second
)

type redditTokenManager struct {
	mu           sync.Mutex
	client       *http.Client
	tokenURL     string
	clientID     string
	clientSecret string
	user         string
	token        string
	expiresAt    time.Time
}

func newRedditTokenManager(client *http.Client) *redditTokenManager {
	user := os.Getenv("REDDIT_USER")
	if user == "" {
		user = "melaven"
	}
	return &redditTokenManager{
		client:       client,
		tokenURL:     redditTokenURL,
		clientID:     os.Getenv("REDDIT_CLIENT_ID"),
		clientSecret: os.Getenv("REDDIT_CLIENT_SECRET"),
		user:         user,
	}
}

func (m *redditTokenManager) userAgent() string {
	return fmt.Sprintf("windows:ovd-highway:v1.0.0 (by /u/%s)", m.user)
}

func (m *redditTokenManager) getToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.token != "" && time.Now().Add(60*time.Second).Before(m.expiresAt) {
		return m.token, nil
	}

	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create Reddit token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(m.clientID, m.clientSecret)

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request Reddit access token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", fmt.Errorf("Reddit token endpoint returned HTTP status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decode Reddit token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("Reddit token response missing access_token")
	}

	m.token = tokenResp.AccessToken
	m.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	return m.token, nil
}

type RedditConnector struct {
	client   *http.Client
	endpoint string
	tokens   *redditTokenManager
	breaker  *breaker.Breaker
}

func NewRedditConnector() *RedditConnector {
	client := &http.Client{Timeout: 20 * time.Second}
	return &RedditConnector{
		client:   client,
		endpoint: redditPostsURL,
		tokens:   newRedditTokenManager(client),
		breaker:  breaker.New(breaker.Options{}),
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
		pollErr := r.breaker.Execute(func() error {
			err := r.poll(ctx, signals)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return err
		})
		if pollErr != nil && ctx.Err() == nil {
			if errors.Is(pollErr, breaker.ErrOpen) {
				log.Printf("reddit connector circuit open, skipping poll")
			} else {
				log.Printf("reddit connector poll failed: %v", pollErr)
			}
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
	token, err := r.tokens.getToken(ctx)
	if err != nil {
		return fmt.Errorf("get Reddit access token: %w", err)
	}

	req, err := http.NewRequest(http.MethodGet, r.endpoint, nil)
	if err != nil {
		return fmt.Errorf("create Reddit request: %w", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", r.tokens.userAgent())

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("request Reddit posts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("Reddit returned HTTP status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var listing redditListing
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&listing); err != nil {
		return fmt.Errorf("decode Reddit posts: %w", err)
	}

	for _, child := range listing.Data.Children {
		post := child.Data
		title := strings.ToLower(post.Title)
		if strings.Contains(title, "[for hire]") || !strings.Contains(title, "[hiring]") {
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
