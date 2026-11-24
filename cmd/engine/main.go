package main

import (
	"context"
	"encoding/json"
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
	"ovd/internal/controlplane"
	"ovd/internal/domain"
	"ovd/internal/lane"
	"ovd/internal/metrics"
	"ovd/internal/onramp"
	"ovd/internal/pipeline/dlq"
	"ovd/internal/store"
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
		"DATABASE_URL",
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

	db, err := store.Init(ctx, env["DATABASE_URL"])
	if err != nil {
		return fmt.Errorf("database initialization: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Warn("database close failed", "err", err)
		}
	}()

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

	// Control plane: persisted engine settings feed the qualifier live
	// (system_prompt + min_score apply on the next incoming signal).
	repo := controlplane.NewPGStore(db)
	settingsStore := controlplane.NewSettingsStore(repo, controlplane.EngineSettings{
		SystemPrompt: lane.DefaultSystemPrompt,
		MinScore:     70,
	}, log)
	if err := settingsStore.Hydrate(ctx); err != nil {
		return fmt.Errorf("hydrate engine settings: %w", err)
	}
	qualifier.SetSettingsProvider(settingsStore)

	// Dead letter queue: archives failed LLM evaluations and releases
	// their dedup keys so the retry worker can re-ingest them.
	archiver, err := dlq.NewPostgresArchive(db)
	if err != nil {
		return fmt.Errorf("dlq archive: %w", err)
	}
	redisDedup, err := dlq.NewRedisDedup(rdb)
	if err != nil {
		return fmt.Errorf("dlq dedup: %w", err)
	}
	dlqManager, err := dlq.NewManager(archiver, redisDedup, dlq.Options{RetryDelay: time.Minute})
	if err != nil {
		return fmt.Errorf("dlq manager: %w", err)
	}
	qualifier.SetFailureHandler(dlqManager)
	mtprotoSrc := onramp.NewMTProtoSource(apiID, env["TG_API_HASH"], env["TG_PHONE"], "session.json", log)
	controlBot, err := onramp.NewControlBot(env["CONTROL_BOT_TOKEN"], adminID, mtprotoSrc, log)
	if err != nil {
		return fmt.Errorf("control bot: %w", err)
	}
	// hh/reddit connectors are no longer static: the control-plane
	// SourceManager spawns them per enabled row in the sources table.
	// mtprotoSrc stays always-on (single session); the manager drives
	// its channel whitelist via TGWhitelist.
	sources := []onramp.Connector{mtprotoSrc}
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

	// Dynamic source lifecycle: hydrates enabled rows from the sources
	// table and reconciles connector goroutines on every API mutation.
	sourceManager := controlplane.NewSourceManager(gctx, repo, rawStream, mtprotoSrc, nil, log)
	if err := sourceManager.Hydrate(gctx); err != nil {
		return fmt.Errorf("hydrate sources: %w", err)
	}
	g.Go(func() error {
		<-gctx.Done()
		sourceManager.Shutdown()
		return nil
	})
	cpHandler := controlplane.NewHandler(repo, repo, settingsStore, sourceManager, log)

	// dispatchLead persists and broadcasts a qualified lead. Shared by
	// the live leadStream consumer and the DLQ retry worker.
	dispatchLead := func(lead domain.QualifiedLead) error {
		if lead.Qualification.IsRelevant && float64(lead.Qualification.Score) >= settingsStore.MinScoreThreshold() {
			if err := store.SaveLead(gctx, db, lead); err != nil {
				metrics.IncDispatchError()
				return fmt.Errorf("save lead %s: %w", lead.Signal.ID, err)
			}
			wsHub.Broadcast(lead)
			metrics.IncLeadSaved()
		} else {
			metrics.IncLeadDropped()
		}
		metrics.IncLeadDispatched()
		log.Debug("lead dispatched", "score", lead.Qualification.Score)
		return nil
	}

	// DLQ retry worker: re-evaluates archived signals synchronously via
	// the qualifier. The worker exclusively owns row deletion (on nil)
	// and retry_count/backoff (on error) — signals are never
	// re-enqueued into rawStream, so backoff state cannot reset.
	retryStore, err := dlq.NewPostgresRetryStore(db)
	if err != nil {
		return fmt.Errorf("dlq retry store: %w", err)
	}
	dlqWorker, err := dlq.NewWorker(retryStore, func(ctx context.Context, rec dlq.RetryRecord) error {
		var sig domain.RawSignal
		if err := json.Unmarshal(rec.Payload, &sig); err != nil {
			return fmt.Errorf("decode dead letter payload: %w", err)
		}
		lead, err := qualifier.RetrySignal(ctx, sig)
		if err != nil {
			return err
		}
		if lead.QualifiedAt.IsZero() {
			return nil // evaluated but rejected — resolve the row
		}
		return dispatchLead(lead)
	}, dlq.WorkerOptions{}, log)
	if err != nil {
		return fmt.Errorf("dlq worker: %w", err)
	}
	g.Go(func() error {
		return dlqWorker.Run(gctx)
	})

	wsAddr := os.Getenv("WS_ADDR")
	if wsAddr == "" {
		wsAddr = ":8080"
	}

	adminToken := os.Getenv("ADMIN_API_TOKEN")
	if adminToken == "" {
		log.Warn("ADMIN_API_TOKEN is not set — /api/v1 and /ws run without auth (dev mode)")
	}

	// CORS wraps the whole mux so OPTIONS preflight succeeds before auth.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.HealthzHandler)
	mux.HandleFunc("GET /readyz", api.ReadyzHandler(map[string]api.ReadinessChecker{
		"postgres": func(ctx context.Context) error {
			return store.Ping(ctx, db)
		},
		"redis": func(ctx context.Context) error {
			return rdb.Ping(ctx).Err()
		},
	}))
	mux.HandleFunc("GET /metrics", api.MetricsHandler(metrics.Default()))
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if adminToken != "" && !api.TokenEqual(api.WSQueryToken(r), adminToken) {
			api.RejectWSAuth(w, r, log)
			return
		}
		api.ServeWS(wsHub, w, r)
	})
	mux.Handle("/api/v1/", api.BearerAuth(adminToken)(cpHandler.Routes()))
	// SPA fallback: any unmatched GET serves the embedded Control Plane
	// build; unknown /api/* and /ws paths get a strict 404.
	mux.Handle("/", api.UIHandler())
	httpSrv := &http.Server{
		Addr:              wsAddr,
		Handler:           api.CORS(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
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
			log.Info("starting onramp connector", "connector", source.Name())
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
				if err := dispatchLead(lead); err != nil {
					if gctx.Err() != nil {
						return nil
					}
					return err
				}
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
