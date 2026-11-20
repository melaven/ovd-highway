package lane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"ovd/internal/domain"
	"ovd/internal/pipeline"
	"ovd/internal/pkg/breaker"
)

const (
	maxInputRunes = 6000
	maxAttempts   = 3
	baseBackoff   = time.Second
	minRelevant   = 4
	minProposal   = 7
)

// SettingsProvider supplies live engine settings (system prompt and
// minimum score threshold) managed by the control plane. It is read on
// every evaluation so updates apply to the next incoming signal
// without a restart. Implemented by controlplane.SettingsStore.
type SettingsProvider interface {
	// SystemPrompt returns the prompt override; empty means default.
	SystemPrompt() string
	// MinScoreThreshold returns the lead acceptance threshold on the
	// LLM's 1-10 score scale (control-plane min_score is 0-100).
	MinScoreThreshold() float64
}

type Qualifier struct {
	llm            Completer
	workers        int
	timeout        time.Duration
	log            *slog.Logger
	releaser       SignalReleaser
	breaker        *breaker.Breaker
	failureHandler FailureHandler
	settings       SettingsProvider
}

type SignalReleaser interface {
	Release(context.Context, domain.RawSignal) error
}

// FailureHandler archives signals whose LLM evaluation failed
// terminally or while the circuit breaker was open. *dlq.Manager
// satisfies this interface.
type FailureHandler interface {
	HandleLLMFailure(ctx context.Context, source string, payload []byte, dedupeKey, errReason string) error
}

// spamFilter drops known spam/low-value signals before they reach the LLM.
var spamFilter = pipeline.NewPreFilter()

func NewQualifier(llm Completer, workers int, timeout time.Duration, log *slog.Logger, releasers ...SignalReleaser) (*Qualifier, error) {
	if llm == nil || log == nil {
		return nil, errors.New("qualifier: llm and logger are required")
	}
	if workers < 1 || timeout <= 0 {
		return nil, errors.New("qualifier: workers must be >= 1 and timeout > 0")
	}
	if len(releasers) > 1 {
		return nil, errors.New("qualifier: at most one signal releaser may be set")
	}
	q := &Qualifier{
		llm:     llm,
		workers: workers,
		timeout: timeout,
		log:     log,
		breaker: breaker.New(breaker.Options{}),
	}
	if len(releasers) == 1 {
		q.releaser = releasers[0]
	}
	return q, nil
}

// SetFailureHandler wires the DLQ handler for terminal LLM failures.
// When set, it takes precedence over the dedup SignalReleaser for
// retryable failures because it also archives the payload.
func (q *Qualifier) SetFailureHandler(h FailureHandler) {
	q.failureHandler = h
}

// SetSettingsProvider wires the live control-plane settings source.
// Pass nil to revert to the built-in defaults.
func (q *Qualifier) SetSettingsProvider(p SettingsProvider) {
	q.settings = p
}

// SetBreaker overrides the default circuit breaker (tests).
func (q *Qualifier) SetBreaker(b *breaker.Breaker) {
	if b != nil {
		q.breaker = b
	}
}

// systemPromptNow resolves the prompt for the next LLM call.
func (q *Qualifier) systemPromptNow() string {
	if q.settings != nil {
		if p := strings.TrimSpace(q.settings.SystemPrompt()); p != "" {
			return p
		}
	}
	return systemPrompt
}

// minScoreThresholdNow resolves the acceptance threshold on the 1-10
// score scale. Defaults to minProposal when no provider is wired.
func (q *Qualifier) minScoreThresholdNow() float64 {
	if q.settings != nil {
		if t := q.settings.MinScoreThreshold(); t >= 0 {
			return t
		}
	}
	return minProposal
}

// Run blocks until ctx is cancelled or in is closed and drained.
// The caller owns both channels and closes out after Run returns.
func (q *Qualifier) Run(ctx context.Context, in <-chan domain.RawSignal, out chan<- domain.QualifiedLead) error {
	g, ctx := errgroup.WithContext(ctx)
	for i := 0; i < q.workers; i++ {
		g.Go(func() error {
			return q.worker(ctx, i, in, out)
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("qualifier pool: %w", err)
	}
	return nil
}

func (q *Qualifier) worker(ctx context.Context, id int, in <-chan domain.RawSignal, out chan<- domain.QualifiedLead) error {
	log := q.log.With("worker", id)
	for {
		select {
		case <-ctx.Done():
			return nil
		case sig, ok := <-in:
			if !ok {
				return nil
			}
			if !PassesPreFilter(sig.Text) {
				log.Debug("signal dropped by prefilter", "signal_id", sig.ID, "source", sig.Source)
				continue
			}
			if ok, reason := spamFilter.Evaluate(sig.Text); !ok {
				log.Debug("signal dropped by spam filter", "signal_id", sig.ID, "reason", reason)
				continue
			}
			qual, err := q.qualify(ctx, sig)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				transient := isRetryable(err) || errors.Is(err, breaker.ErrOpen)
				if transient && q.failureHandler != nil {
					q.archiveFailure(ctx, log, sig, err)
				} else if transient && q.releaser != nil {
					if releaseErr := q.releaser.Release(ctx, sig); releaseErr != nil {
						log.Error("failed to release dedup key after transient llm failure", "signal_id", sig.ID, "err", releaseErr)
					} else {
						log.Warn("released dedup key after transient llm failure", "signal_id", sig.ID)
					}
				}
				log.Error("qualify failed, signal dropped", "signal_id", sig.ID, "source", sig.Source, "err", err)
				continue
			}
			lead, ok := q.leadFrom(sig, qual)
			if !ok {
				log.Debug("signal rejected", "signal_id", sig.ID, "score", qual.Score)
				continue
			}
			select {
			case out <- lead:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (q *Qualifier) qualify(ctx context.Context, sig domain.RawSignal) (domain.Qualification, error) {
	text := truncateRunes(sig.Text, maxInputRunes)

	var raw string
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		raw, err = q.callOnce(ctx, text, sig.JobType)
		if err == nil {
			break
		}
		if !isRetryable(err) || attempt == maxAttempts-1 {
			return domain.Qualification{}, fmt.Errorf("llm call: %w", err)
		}
		delay := baseBackoff<<attempt + time.Duration(rand.Int64N(int64(baseBackoff)))
		q.log.Warn("llm call failed, retrying", "signal_id", sig.ID, "attempt", attempt+1, "delay", delay, "err", err)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return domain.Qualification{}, fmt.Errorf("retry wait: %w", ctx.Err())
		}
	}

	qual, err := parseQualification(raw)
	if err != nil {
		return domain.Qualification{}, fmt.Errorf("parse llm output: %w", err)
	}
	qual = normalize(qual, q.minScoreThresholdNow())
	if qual.DetectedJobType == "other" {
		// Garbage (CVs, course ads, spam): drop silently before it reaches WS.
		qual.IsRelevant = false
		qual.Score = 0
		qual.DraftProposal = ""
	}
	return qual, nil
}

// leadFrom applies the relevance thresholds and builds the lead.
// ok=false means the signal was evaluated but rejected.
func (q *Qualifier) leadFrom(sig domain.RawSignal, qual domain.Qualification) (domain.QualifiedLead, bool) {
	if !qual.IsRelevant || float64(qual.Score) < q.minScoreThresholdNow() {
		return domain.QualifiedLead{}, false
	}
	jobType := sig.JobType
	switch qual.DetectedJobType {
	case "freelance", "vacancy":
		jobType = qual.DetectedJobType
	}
	return domain.QualifiedLead{Signal: sig, Qualification: qual, QualifiedAt: time.Now().UTC(), JobType: jobType}, true
}

// signalMarker is implemented by Deduplicator to re-arm dedup keys
// after a successful DLQ retry.
type signalMarker interface {
	MarkSeen(context.Context, domain.RawSignal) error
}

// RetrySignal synchronously re-evaluates a dead-lettered signal: it
// runs the same pre-filters, circuit breaker and LLM call as the live
// worker path, then re-arms the dedup key so live traffic won't
// duplicate it.
//
// It deliberately does NOT invoke the FailureHandler: the DLQ worker
// owns retry_count/backoff and row deletion, so failures are returned
// to it as plain errors.
//
// A zero QualifiedLead with nil error means the signal was evaluated
// but rejected (or dropped by a pre-filter) — the row is resolved.
func (q *Qualifier) RetrySignal(ctx context.Context, sig domain.RawSignal) (domain.QualifiedLead, error) {
	log := q.log.With("signal_id", sig.ID, "retry", true)
	if !PassesPreFilter(sig.Text) {
		log.Debug("retry dropped by prefilter")
		return domain.QualifiedLead{}, nil
	}
	if ok, reason := spamFilter.Evaluate(sig.Text); !ok {
		log.Debug("retry dropped by spam filter", "reason", reason)
		return domain.QualifiedLead{}, nil
	}

	qual, err := q.qualify(ctx, sig)
	if err != nil {
		return domain.QualifiedLead{}, err
	}

	// Evaluation succeeded: re-arm dedup so the connectors' next poll
	// of the same post is dropped instead of duplicating this lead.
	if marker, ok := q.releaser.(signalMarker); ok {
		if err := marker.MarkSeen(ctx, sig); err != nil {
			log.Warn("failed to re-arm dedup key after dlq retry", "err", err)
		}
	}

	lead, ok := q.leadFrom(sig, qual)
	if !ok {
		log.Debug("retry rejected", "score", qual.Score)
		return domain.QualifiedLead{}, nil
	}
	return lead, nil
}

// archiveFailure sends a failed signal to the DLQ. The DLQ releases
// the dedup key itself after archiving, so no separate Release is needed.
func (q *Qualifier) archiveFailure(ctx context.Context, log *slog.Logger, sig domain.RawSignal, cause error) {
	payload, err := json.Marshal(sig)
	if err != nil {
		log.Error("marshal signal for dlq failed", "signal_id", sig.ID, "err", err)
		return
	}
	if err := q.failureHandler.HandleLLMFailure(ctx, sig.Source, payload, DedupKey(sig), cause.Error()); err != nil {
		log.Error("dlq handle failure failed", "signal_id", sig.ID, "err", err)
	}
}

func (q *Qualifier) callOnce(ctx context.Context, text, jobType string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, q.timeout)
	defer cancel()
	var raw string
	var callErr error
	err := q.breaker.Execute(func() error {
		var e error
		raw, e = q.llm.Complete(ctx, q.systemPromptNow(), userMessage(text, jobType))
		callErr = e
		if e != nil && !isRetryable(e) {
			// Permanent errors (e.g. HTTP 400) must not trip the breaker.
			return nil
		}
		return e
	})
	if err != nil {
		return "", err
	}
	return raw, callErr
}

func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code == 429 || (se.Code >= 500 && se.Code < 600)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne)
}

func parseQualification(raw string) (domain.Qualification, error) {
	start, end := strings.IndexByte(raw, '{'), strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return domain.Qualification{}, errors.New("no JSON object in output")
	}
	var qual domain.Qualification
	if err := json.Unmarshal([]byte(raw[start:end+1]), &qual); err != nil {
		return domain.Qualification{}, fmt.Errorf("unmarshal: %w", err)
	}
	if qual.Score < 1 || qual.Score > 10 {
		return domain.Qualification{}, fmt.Errorf("score out of range: %d", qual.Score)
	}
	return qual, nil
}

// normalize enforces the invariants the prompt only requests.
func normalize(qual domain.Qualification, proposalThreshold float64) domain.Qualification {
	if qual.Score < minRelevant {
		qual.IsRelevant = false
	}
	if !qual.IsRelevant || float64(qual.Score) < proposalThreshold {
		qual.DraftProposal = ""
	}
	if strings.TrimSpace(qual.EstimatedBudget) == "" {
		qual.EstimatedBudget = "Не указан"
	}
	if qual.TechStack == nil {
		qual.TechStack = []string{}
	}
	return qual
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
