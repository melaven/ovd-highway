package metrics

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestRegistryConcurrencyAndExposition(t *testing.T) {
	reg := NewRegistry()

	const workers = 16
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for range iterations {
				reg.IncLeadDispatched()
				reg.IncLeadSaved()
				reg.IncLeadDropped()
				reg.IncDispatchError()
				reg.IncDLQRetryScheduled()
				reg.IncDLQResolved()
				reg.IncDLQStoreError()
			}
		}()
	}
	wg.Wait()

	var buf bytes.Buffer
	if err := reg.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus failed: %v", err)
	}

	out := buf.String()
	expected := []string{
		"# TYPE ovd_uptime_seconds gauge",
		"# TYPE ovd_goroutines gauge",
		"ovd_leads_dispatched_total 1600",
		"ovd_leads_saved_total 1600",
		"ovd_leads_dropped_total 1600",
		"ovd_dispatch_errors_total 1600",
		"ovd_dlq_retries_scheduled_total 1600",
		"ovd_dlq_resolved_total 1600",
		"ovd_dlq_store_errors_total 1600",
	}

	for _, line := range expected {
		if !strings.Contains(out, line) {
			t.Errorf("expected output to contain %q, got:\n%s", line, out)
		}
	}
}
