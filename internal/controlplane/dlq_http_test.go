package controlplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ovd/internal/api"
	"ovd/internal/pipeline/dlq"
)

type fakeDLQAdmin struct {
	items    []dlq.DLQItem
	count    int
	queued   int64
	listErr  error
	countErr error
	retryErr error
	gotLimit int
}

func (f *fakeDLQAdmin) List(_ context.Context, limit int) ([]dlq.DLQItem, error) {
	f.gotLimit = limit
	return f.items, f.listErr
}

func (f *fakeDLQAdmin) Count(context.Context) (int, error) {
	return f.count, f.countErr
}

func (f *fakeDLQAdmin) RetryAll(context.Context) (int64, error) {
	return f.queued, f.retryErr
}

func TestListDLQReturnsCountAndItems(t *testing.T) {
	env := newTestEnv(t)
	env.handler.SetDLQAdmin(&fakeDLQAdmin{
		count: 2,
		items: []dlq.DLQItem{
			{ID: "a", Source: "telegram_mtproto", Error: "llm http status 503", Attempts: 1, CreatedAt: time.Now(), PayloadPreview: "{...}"},
			{ID: "b", Source: "reddit", Error: "timeout", Attempts: 0, CreatedAt: time.Now()},
		},
	})

	rec := do(t, env, http.MethodGet, "/api/v1/dlq", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Count int           `json:"count"`
		Items []dlq.DLQItem `json:"items"`
	}
	decodeJSON(t, rec, &body)
	if body.Count != 2 || len(body.Items) != 2 {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.Items[0].ID != "a" || body.Items[0].Attempts != 1 {
		t.Fatalf("unexpected item: %+v", body.Items[0])
	}
}

func TestListDLQLimitParam(t *testing.T) {
	tests := []struct {
		name string
		path string
		want int
	}{
		{"no param defaults to 50", "/api/v1/dlq", 50},
		{"valid limit", "/api/v1/dlq?limit=10", 10},
		{"above max clamps to 100", "/api/v1/dlq?limit=999", 100},
		{"non-numeric falls back to 50", "/api/v1/dlq?limit=abc", 50},
		{"non-positive falls back to 50", "/api/v1/dlq?limit=0", 50},
		{"negative falls back to 50", "/api/v1/dlq?limit=-5", 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t)
			admin := &fakeDLQAdmin{}
			env.handler.SetDLQAdmin(admin)

			rec := do(t, env, http.MethodGet, tt.path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if admin.gotLimit != tt.want {
				t.Fatalf("List limit = %d, want %d", admin.gotLimit, tt.want)
			}
		})
	}
}

func TestListDLQEmptyReturnsEmptyArray(t *testing.T) {
	env := newTestEnv(t)
	env.handler.SetDLQAdmin(&fakeDLQAdmin{count: 0, items: nil})

	rec := do(t, env, http.MethodGet, "/api/v1/dlq", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("items should be [], body=%s", rec.Body.String())
	}
}

func TestRetryDLQQueuesRows(t *testing.T) {
	env := newTestEnv(t)
	env.handler.SetDLQAdmin(&fakeDLQAdmin{queued: 7})

	rec := do(t, env, http.MethodPost, "/api/v1/dlq/retry", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Queued int64 `json:"queued"`
	}
	decodeJSON(t, rec, &body)
	if body.Queued != 7 {
		t.Fatalf("queued = %d, want 7", body.Queued)
	}
}

func TestDLQEndpointsUnavailableWithoutStore(t *testing.T) {
	env := newTestEnv(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/dlq"},
		{http.MethodPost, "/api/v1/dlq/retry"},
	} {
		rec := do(t, env, tc.method, tc.path, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: status = %d, want 503", tc.method, tc.path, rec.Code)
		}
	}
}

func TestDLQEndpointsRejectWithoutToken(t *testing.T) {
	env := newTestEnv(t)
	env.handler.SetDLQAdmin(&fakeDLQAdmin{})

	// Mount the same way production does: behind BearerAuth.
	wrapped := api.BearerAuth("secret")(env.handler.Routes())
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/dlq"},
		{http.MethodPost, "/api/v1/dlq/retry"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token: status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dlq", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with token: status = %d, want 200", rec.Code)
	}
}
