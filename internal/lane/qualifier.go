package lane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"ovd/internal/domain"
)

const (
	maxInputRunes = 6000
	maxAttempts   = 3
	baseBackoff   = time.Second
	minRelevant   = 4
	minProposal   = 7
)

type Qualifier struct {
	llm     Completer
	workers int
	timeout time.Duration
	log     *slog.Logger
}

func NewQualifier(llm Completer, workers int, timeout time.Duration, log *slog.Logger) (*Qualifier, error) {
	if llm == nil || log == nil {
		return nil, errors.New("qualifier: llm and logger are required")
	}
	if workers < 1 || timeout <= 0 {
		return nil, errors.New("qualifier: workers must be >= 1 and timeout > 0")
	}
	return &Qualifier{llm: llm, workers: workers, timeout: timeout, log: log}, nil
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
			qual, err := q.qualify(ctx, sig)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				log.Error("qualify failed, signal dropped", "signal_id", sig.ID, "source", sig.Source, "err", err)
				continue
			}
			if !qual.IsRelevant || qual.Score < minProposal {
				log.Debug("signal rejected", "signal_id", sig.ID, "score", qual.Score)
				continue
			}
			jobType := sig.JobType
			switch qual.DetectedJobType {
			case "freelance", "vacancy":
				jobType = qual.DetectedJobType
			}
			lead := domain.QualifiedLead{Signal: sig, Qualification: qual, QualifiedAt: time.Now().UTC(), JobType: jobType}
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
	qual = normalize(qual)
	if qual.DetectedJobType == "other" {
		// Garbage (CVs, course ads, spam): drop silently before it reaches WS.
		qual.IsRelevant = false
		qual.Score = 0
		qual.DraftProposal = ""
	}
	return qual, nil
}

func (q *Qualifier) callOnce(ctx context.Context, text, jobType string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, q.timeout)
	defer cancel()
	return q.llm.Complete(ctx, systemPrompt, userMessage(text, jobType))
}

func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code == 429 || se.Code >= 500
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
func normalize(qual domain.Qualification) domain.Qualification {
	if qual.Score < minRelevant {
		qual.IsRelevant = false
	}
	if !qual.IsRelevant || qual.Score < minProposal {
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
