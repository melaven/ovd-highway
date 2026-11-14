package lane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"ovd/internal/domain"
)

type Deduplicator struct {
	rdb *redis.Client
	ttl time.Duration
	log *slog.Logger
}

func NewDeduplicator(rdb *redis.Client, ttl time.Duration, log *slog.Logger) *Deduplicator {
	return &Deduplicator{rdb: rdb, ttl: ttl, log: log.With("component", "deduplicator")}
}

func (d *Deduplicator) Run(ctx context.Context, in <-chan domain.RawSignal, out chan<- domain.RawSignal) error {
	d.log.Info("deduplicator lane started")
	for {
		select {
		case <-ctx.Done():
			return nil
		case sig, ok := <-in:
			if !ok {
				return nil
			}

			isNew, err := d.check(ctx, sig)
			if err != nil {
				d.log.Error("redis dedup failed, bypassing", "signal_id", sig.ID, "err", err)
			} else if !isNew {
				d.log.Debug("duplicate dropped", "signal_id", sig.ID, "source", sig.Source)
				continue
			}

			select {
			case out <- sig:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (d *Deduplicator) check(ctx context.Context, sig domain.RawSignal) (bool, error) {
	norm := strings.Join(strings.Fields(strings.ToLower(sig.Text)), " ")
	hash := sha256.Sum256([]byte(norm))
	key := fmt.Sprintf("dedup:%s:%s", sig.Source, hex.EncodeToString(hash[:]))

	return d.rdb.SetNX(ctx, key, sig.ID, d.ttl).Result()
}
