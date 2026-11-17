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

var releaseDedupKeyScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

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
	return d.rdb.SetNX(ctx, dedupKey(sig), sig.ID, d.ttl).Result()
}

func (d *Deduplicator) Release(ctx context.Context, sig domain.RawSignal) error {
	if err := releaseDedupKeyScript.Run(ctx, d.rdb, []string{dedupKey(sig)}, sig.ID).Err(); err != nil {
		return fmt.Errorf("release dedup key: %w", err)
	}
	return nil
}

func dedupKey(sig domain.RawSignal) string {
	norm := strings.Join(strings.Fields(strings.ToLower(sig.Text)), " ")
	hash := sha256.Sum256([]byte(norm))
	return fmt.Sprintf("dedup:%s:%s", sig.Source, hex.EncodeToString(hash[:]))
}
