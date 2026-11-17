package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/joho/godotenv/autoload"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"

	"ovd/internal/api"
	"ovd/internal/domain"
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
	_ = godotenv.Load()

	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	env, err := requireEnv(
		"REDIS_URL",
		"LLM_API_KEY",
		"TG_API_ID",
		"TG_API_HASH",
		"TG_PHONE",
		"CONTROL_BOT_TOKEN",
		"ADMIN_TG_ID",
	)
	if err != nil {
		return err
	}
	apiID, err := strconv.Atoi(env["TG_API_ID"])
	if err != nil {
		return fmt.Errorf("parse TG_API_ID: %w", err)
	}
	adminID, err := strconv.ParseInt(env["ADMIN_TG_ID"], 10, 64)
	if err != nil {
		return fmt.Errorf("parse ADMIN_TG_ID: %w", err)
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

	var llm lane.Completer
	if baseURL := os.Getenv("LLM_BASE_URL"); baseURL != "" {
		model := os.Getenv("LLM_MODEL")
		if model == "" {
			model = lane.DefaultModel
		}
		llm, err = lane.NewOpenAICompatibleClient(env["LLM_API_KEY"], baseURL, model)
	} else {
		llm, err = lane.NewOpenRouterClient(env["LLM_API_KEY"], os.Getenv("LLM_MODEL"))
	}
	if err != nil {
		return fmt.Errorf("LLM client: %w", err)
	}
	dedup := lane.NewDeduplicator(rdb, dedupTTL, log)
	qualifier, err := lane.NewQualifier(llm, qualifierWorkers, qualifierTimeout, log, dedup)
	if err != nil {
		return fmt.Errorf("qualifier: %w", err)
	}
	habr := onramp.NewHabrSource(log)
	mtprotoSrc := onramp.NewMTProtoSource(apiID, env["TG_API_HASH"], env["TG_PHONE"], "session.json", log)
	controlBot, err := onramp.NewControlBot(env["CONTROL_BOT_TOKEN"], adminID, mtprotoSrc, log)
	if err != nil {
		return fmt.Errorf("control bot: %w", err)
	}
	sources := []onramp.Source{habr, mtprotoSrc}
	if os.Getenv("MOCK_ONRAMP") == "1" {
		sources = append(sources, onramp.NewMockSource(log))
	}
	// Telegram notifier is disabled while setting up the MTProto session:
	// notifier, err := exit.NewTelegramNotifier(env["TG_BOT_TOKEN"], chatID, log)
	// g.Go(func() error { return notifier.Run(gctx, leadStream) })

	// Pipeline channels.
	rawStream := make(chan domain.RawSignal, 100)
	dedupStream := make(chan domain.RawSignal, 100)
	leadStream := make(chan domain.QualifiedLead, 50)

	g, gctx := errgroup.WithContext(ctx)

	wsHub := api.NewHub(log)
	g.Go(func() error {
		wsHub.Run(gctx)
		return nil
	})

	wsAddr := os.Getenv("WS_ADDR")
	if wsAddr == "" {
		wsAddr = ":8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		api.ServeWS(wsHub, w, r)
	})
	httpSrv := &http.Server{
		Addr:              wsAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	g.Go(func() error {
		<-gctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	})
	g.Go(func() error {
		log.Info("ws server listening", "addr", wsAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("ws server: %w", err)
		}
		return nil
	})

	for _, source := range sources {
		source := source
		g.Go(func() error {
			return source.Start(gctx, rawStream)
		})
	}
	g.Go(func() error {
		controlBot.Run(gctx)
		return nil
	})
	g.Go(stage(dedupStream, func() error { return dedup.Run(gctx, rawStream, dedupStream) }))
	g.Go(stage(leadStream, func() error { return qualifier.Run(gctx, dedupStream, leadStream) }))
	g.Go(func() error {
		for {
			select {
			case <-gctx.Done():
				return nil
			case lead, ok := <-leadStream:
				if !ok {
					return nil
				}
				if lead.Qualification.IsRelevant && lead.Qualification.Score >= 7 {
					wsHub.Broadcast(lead)
				}
				log.Debug("lead dispatched", "score", lead.Qualification.Score)
			}
		}
	})

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
