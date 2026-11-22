package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ovd/internal/metrics"
)

func TestMetricsHandler(t *testing.T) {
	reg := metrics.NewRegistry()
	reg.IncLeadDispatched()
	reg.IncLeadSaved()
	reg.IncDLQResolved()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	MetricsHandler(reg).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"ovd_uptime_seconds",
		"ovd_leads_dispatched_total 1",
		"ovd_leads_saved_total 1",
		"ovd_dlq_resolved_total 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected body to contain %q, got:\n%s", want, body)
		}
	}
}
