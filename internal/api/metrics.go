package api

import (
	"net/http"

	"ovd/internal/metrics"
)

func MetricsHandler(reg *metrics.Registry) http.HandlerFunc {
	if reg == nil {
		reg = metrics.Default()
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_ = reg.WritePrometheus(w)
	}
}
