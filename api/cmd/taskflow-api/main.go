// Command taskflow-api runs the TaskFlow REST API.
//
// Usage:
//
//	taskflow-api [serve]                    start the HTTP server (default)
//	taskflow-api migrate [up|status|down]   apply or inspect schema migrations (needs DATABASE_URL)
//	taskflow-api healthcheck                exit 0 if the local server answers /healthz (for distroless images)
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/TavoA2/taskflow-app/api/internal/config"
	"github.com/TavoA2/taskflow-app/api/internal/httpapi"
	"github.com/TavoA2/taskflow-app/api/internal/migrations"
	"github.com/TavoA2/taskflow-app/api/internal/store"
)

// version is set at build time: -ldflags "-X main.version=<git sha>".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})).
		With("service", "taskflow-api", "version", version)

	switch cmd {
	case "serve":
		return serve(cfg, logger)
	case "migrate":
		action := "up"
		if len(args) > 1 {
			action = args[1]
		}
		return migrate(cfg, logger, action)
	case "healthcheck":
		return healthcheck(cfg)
	default:
		return fmt.Errorf("unknown command %q (want serve, migrate or healthcheck)", cmd)
	}
}

func migrate(cfg config.Config, logger *slog.Logger, action string) error {
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required for migrate")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	logger = logger.With("component", "migrate")
	if err := waitForDB(ctx, db, logger, 30*time.Second); err != nil {
		return err
	}
	return migrations.Run(ctx, db, action, logger)
}

// waitForDB retries the first connection for up to timeout. A database that is still starting
// (compose cold start, or a Kubernetes Job scheduled next to a new Postgres pod) is expected,
// not fatal; a database that never comes up still fails the step clearly.
func waitForDB(ctx context.Context, db *sql.DB, logger *slog.Logger, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for attempt := 1; ; attempt++ {
		pingCtx, pingCancel := context.WithTimeout(ctx, 3*time.Second)
		err := db.PingContext(pingCtx)
		pingCancel()
		if err == nil {
			if attempt > 1 {
				logger.Info("database reachable", "attempts", attempt)
			}
			return nil
		}
		logger.Warn("database not reachable yet, retrying", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("database not reachable after %s: %w", timeout, err)
		case <-time.After(time.Second):
		}
	}
}

func serve(cfg config.Config, logger *slog.Logger) error {
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required for serve")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The pool connects lazily: the server starts even if the database is still coming up.
	// Pool size and timeouts can be tuned with DATABASE_URL parameters (e.g. pool_max_conns).
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database pool: %w", err)
	}
	defer pool.Close()

	// A dedicated registry (not the global default) exposes exactly what is registered here.
	reg := prometheus.NewRegistry()
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "taskflow_build_info",
		Help: "Always 1; labels identify the running build.",
	}, []string{"version", "goversion"})
	buildInfo.WithLabelValues(version, runtime.Version()).Set(1)
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		store.NewPoolCollector(pool),
		buildInfo,
	)
	readiness := httpapi.NewReadiness(pool, logger)

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Logger:    logger,
			Store:     store.NewPostgres(pool),
			Readiness: readiness,
			Metrics:   httpapi.NewMetrics(reg),
			Gatherer:  reg,
		}),
		ReadHeaderTimeout: 5 * time.Second, // mitigates slow-header (Slowloris) clients
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	// Restore default signal handling: a second SIGINT/SIGTERM terminates immediately.
	stop()

	// Keep serving while Kubernetes removes this pod from Service endpoints; that removal runs
	// concurrently with SIGTERM, so closing at once would drop requests still being routed here.
	// /readyz fails from now on, which also takes the pod out of rotation where SIGTERM alone does not.
	readiness.StartShutdown()
	logger.Info("shutdown signal received, draining", "drain_delay", cfg.DrainDelay.String())
	time.Sleep(cfg.DrainDelay)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

func healthcheck(cfg config.Config) error {
	_, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("HTTP_ADDR %q: %w", cfg.HTTPAddr, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}
