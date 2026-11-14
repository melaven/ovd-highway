package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"

	"ovd/internal/domain"
	"ovd/internal/exit"
	"ovd/internal/lane"
	"ovd/internal/onramp"
)

const (
	qualifierWorkers = 4
	qualifierTimeout = 45 * time.Second
	dedupTTL         = 72 * time.Hour
)

func main() {
	if err := run(); err != nil {
		slog.Error("engine terminated", "err", err)
		os.Exit(1)
	}
}

func run() error {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	env, err := requireEnv("REDIS_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "TG_BOT_TOKEN", "TG_CHAT_ID")
	if err != nil {
		return err
	}
	chatID, err := strconv.ParseInt(env["TG_CHAT_ID"], 10, 64)
	if err != nil {
		return fmt.Errorf("parse TG_CHAT_ID: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Dependencies.
	redisOpt, err := redis.ParseURL(env["REDIS_URL"])
	if err != nil {
		return fmt.Errorf("parse REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(redisOpt)
	defer func() {
		if err := rdb.Close(); err != nil {
			log.Warn("redis close failed", "err", err)
		}
	}()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = rdb.Ping(pingCtx).Err()
	cancel()
	if err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}

	llm, err := lane.NewAnthropicFromEnv()
	if err != nil {
		return fmt.Errorf("anthropic client: %w", err)
	}
	qualifier, err := lane.NewQualifier(llm, qualifierWorkers, qualifierTimeout, log)
	if err != nil {
		return fmt.Errorf("qualifier: %w", err)
	}
	dedup := lane.NewDeduplicator(rdb, dedupTTL, log)
	habr := onramp.NewHabrSource(log)
	sources := []onramp.Source{habr}
	notifier, err := exit.NewTelegramNotifier(env["TG_BOT_TOKEN"], chatID, log)
	if err != nil {
		return fmt.Errorf("telegram notifier: %w", err)
	}

	// Pipeline channels.
	rawStream := make(chan domain.RawSignal, 100)
	dedupStream := make(chan domain.RawSignal, 100)
	leadStream := make(chan domain.QualifiedLead, 50)

	g, gctx := errgroup.WithContext(ctx)
	for _, source := range sources {
		source := source
		g.Go(func() error {
			return source.Start(gctx, rawStream)
		})
	}
	g.Go(stage(dedupStream, func() error { return dedup.Run(gctx, rawStream, dedupStream) }))
	g.Go(stage(leadStream, func() error { return qualifier.Run(gctx, dedupStream, leadStream) }))
	g.Go(func() error { return notifier.Run(gctx, leadStream) })

	log.Info("highway started")
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("pipeline: %w", err)
	}
	log.Info("highway stopped")
	return nil
}

// stage closes out when fn returns, so the close cascades downstream
// and each consumer drains naturally.
func stage[T any](out chan T, fn func() error) func() error {
	return func() error {
		defer close(out)
		return fn()
	}
}

func requireEnv(keys ...string) (map[string]string, error) {
	env := make(map[string]string, len(keys))
	var missing []string
	for _, k := range keys {
		v := os.Getenv(k)
		if v == "" {
			missing = append(missing, k)
			continue
		}
		env[k] = v
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing env vars: %s", strings.Join(missing, ", "))
	}
	return env, nil
}
