package api

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// ReadinessChecker verifies a single dependency. It must respect ctx.
type ReadinessChecker func(ctx context.Context) error

type readyResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

const readyzCheckTimeout = 2 * time.Second

// ReadyzHandler runs every named check concurrently with a 2s timeout.
// It responds 200 "ready" when all checks pass, otherwise 503
// "not_ready" with each check's result ("ok" or the error text).
func ReadyzHandler(checks map[string]ReadinessChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results := make(map[string]string, len(checks))
		var mu sync.Mutex
		var wg sync.WaitGroup

		for name, check := range checks {
			wg.Add(1)
			go func(name string, check ReadinessChecker) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(r.Context(), readyzCheckTimeout)
				defer cancel()
				err := check(ctx)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					results[name] = err.Error()
				} else {
					results[name] = "ok"
				}
			}(name, check)
		}
		wg.Wait()

		status := "ready"
		code := http.StatusOK
		for _, res := range results {
			if res != "ok" {
				status = "not_ready"
				code = http.StatusServiceUnavailable
				break
			}
		}
		WriteJSON(w, code, readyResponse{Status: status, Checks: results})
	}
}
