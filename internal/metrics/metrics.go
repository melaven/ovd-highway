package metrics

import (
	"fmt"
	"io"
	"runtime"
	"sync/atomic"
	"time"
)

type Registry struct {
	startedAt time.Time

	leadsDispatched     atomic.Uint64
	leadsSaved          atomic.Uint64
	leadsDropped        atomic.Uint64
	dispatchErrors      atomic.Uint64
	dlqRetriesScheduled atomic.Uint64
	dlqResolved         atomic.Uint64
	dlqStoreErrors      atomic.Uint64
}

func NewRegistry() *Registry {
	return &Registry{
		startedAt: time.Now(),
	}
}

var defaultRegistry = NewRegistry()

func Default() *Registry {
	return defaultRegistry
}

func (r *Registry) IncLeadDispatched()    { r.leadsDispatched.Add(1) }
func (r *Registry) IncLeadSaved()         { r.leadsSaved.Add(1) }
func (r *Registry) IncLeadDropped()       { r.leadsDropped.Add(1) }
func (r *Registry) IncDispatchError()     { r.dispatchErrors.Add(1) }
func (r *Registry) IncDLQRetryScheduled() { r.dlqRetriesScheduled.Add(1) }
func (r *Registry) IncDLQResolved()       { r.dlqResolved.Add(1) }
func (r *Registry) IncDLQStoreError()     { r.dlqStoreErrors.Add(1) }

func IncLeadDispatched()    { defaultRegistry.IncLeadDispatched() }
func IncLeadSaved()         { defaultRegistry.IncLeadSaved() }
func IncLeadDropped()       { defaultRegistry.IncLeadDropped() }
func IncDispatchError()     { defaultRegistry.IncDispatchError() }
func IncDLQRetryScheduled() { defaultRegistry.IncDLQRetryScheduled() }
func IncDLQResolved()       { defaultRegistry.IncDLQResolved() }
func IncDLQStoreError()     { defaultRegistry.IncDLQStoreError() }

func (r *Registry) WritePrometheus(w io.Writer) error {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	uptime := time.Since(r.startedAt).Seconds()
	if uptime < 0 {
		uptime = 0
	}

	_, err := fmt.Fprintf(w,
		"# HELP ovd_uptime_seconds Process uptime in seconds.\n"+
			"# TYPE ovd_uptime_seconds gauge\n"+
			"ovd_uptime_seconds %.3f\n"+
			"# HELP ovd_goroutines Current number of active goroutines.\n"+
			"# TYPE ovd_goroutines gauge\n"+
			"ovd_goroutines %d\n"+
			"# HELP ovd_mem_alloc_bytes Bytes of allocated heap objects.\n"+
			"# TYPE ovd_mem_alloc_bytes gauge\n"+
			"ovd_mem_alloc_bytes %d\n"+
			"# HELP ovd_mem_sys_bytes Total bytes of memory obtained from the OS.\n"+
			"# TYPE ovd_mem_sys_bytes gauge\n"+
			"ovd_mem_sys_bytes %d\n"+
			"# HELP ovd_gc_cycles_total Completed GC cycles.\n"+
			"# TYPE ovd_gc_cycles_total counter\n"+
			"ovd_gc_cycles_total %d\n"+
			"# HELP ovd_leads_dispatched_total Total qualified leads processed by dispatcher.\n"+
			"# TYPE ovd_leads_dispatched_total counter\n"+
			"ovd_leads_dispatched_total %d\n"+
			"# HELP ovd_leads_saved_total Relevant leads persisted and broadcast.\n"+
			"# TYPE ovd_leads_saved_total counter\n"+
			"ovd_leads_saved_total %d\n"+
			"# HELP ovd_leads_dropped_total Leads filtered out by relevance or score threshold.\n"+
			"# TYPE ovd_leads_dropped_total counter\n"+
			"ovd_leads_dropped_total %d\n"+
			"# HELP ovd_dispatch_errors_total Errors encountered while dispatching leads.\n"+
			"# TYPE ovd_dispatch_errors_total counter\n"+
			"ovd_dispatch_errors_total %d\n"+
			"# HELP ovd_dlq_retries_scheduled_total Dead-letter records scheduled for retry.\n"+
			"# TYPE ovd_dlq_retries_scheduled_total counter\n"+
			"ovd_dlq_retries_scheduled_total %d\n"+
			"# HELP ovd_dlq_resolved_total Dead-letter records successfully resolved.\n"+
			"# TYPE ovd_dlq_resolved_total counter\n"+
			"ovd_dlq_resolved_total %d\n"+
			"# HELP ovd_dlq_store_errors_total Internal DLQ persistence errors.\n"+
			"# TYPE ovd_dlq_store_errors_total counter\n"+
			"ovd_dlq_store_errors_total %d\n",
		uptime,
		runtime.NumGoroutine(),
		ms.Alloc,
		ms.Sys,
		ms.NumGC,
		r.leadsDispatched.Load(),
		r.leadsSaved.Load(),
		r.leadsDropped.Load(),
		r.dispatchErrors.Load(),
		r.dlqRetriesScheduled.Load(),
		r.dlqResolved.Load(),
		r.dlqStoreErrors.Load(),
	)
	return err
}
