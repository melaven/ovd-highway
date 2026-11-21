package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadyzHandler(t *testing.T) {
	ok := func(context.Context) error { return nil }
	fail := func(context.Context) error { return errors.New("connection refused") }

	tests := []struct {
		name         string
		checks       map[string]ReadinessChecker
		wantCode     int
		wantStatus   string
		wantCheckKey string
		wantCheckVal string
	}{
		{
			name: "all dependencies healthy",
			checks: map[string]ReadinessChecker{
				"postgres": ok,
				"redis":    ok,
			},
			wantCode:   http.StatusOK,
			wantStatus: "ready",
		},
		{
			name: "one dependency fails",
			checks: map[string]ReadinessChecker{
				"postgres": ok,
				"redis":    fail,
			},
			wantCode:     http.StatusServiceUnavailable,
			wantStatus:   "not_ready",
			wantCheckKey: "redis",
			wantCheckVal: "connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

			ReadyzHandler(tt.checks)(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantCode, rec.Body.String())
			}
			var body readyResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}
			if body.Status != tt.wantStatus {
				t.Fatalf("status field = %q, want %q", body.Status, tt.wantStatus)
			}
			if tt.wantCheckKey != "" {
				if got := body.Checks[tt.wantCheckKey]; got != tt.wantCheckVal {
					t.Fatalf("checks[%q] = %q, want %q", tt.wantCheckKey, got, tt.wantCheckVal)
				}
			}
			if body.Checks["postgres"] != "ok" {
				t.Fatalf("checks[postgres] = %q, want ok", body.Checks["postgres"])
			}
		})
	}
}
