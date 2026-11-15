package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Zuneve/template/internal/config"
	"github.com/Zuneve/template/internal/container"
	"github.com/Zuneve/template/internal/http/server"
	"github.com/Zuneve/template/internal/infra/telemetry"
)

// version is set at build time: go build -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := telemetry.NewLogger(os.Stdout, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	otel, err := telemetry.Setup(ctx, version, logger)
	if err != nil {
		return err
	}

	app, err := container.New(ctx, cfg, logger, otel)
	if err != nil {
		_ = otel.Shutdown(context.Background())
		return err
	}

	srv := server.New(cfg.HTTP.Addr, app.Handler())

	// The background components must outlive the signal: main stops them
	// itself, after the HTTP server, so the order below is the order of the
	// shutdown.
	app.StartBackground(context.WithoutCancel(ctx))

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()
	logger.InfoContext(ctx, "http server started",
		slog.String("addr", cfg.HTTP.Addr),
		slog.String("version", version),
	)

	var serveFailure error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveFailure = fmt.Errorf("http server: %w", err)
		}
	}

	// One budget for the whole shutdown: whatever the server does not use is
	// left for the workers to drain the queue.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// 1. No new HTTP requests; the ones in flight finish (they may still cancel queued pushes).
	logger.InfoContext(shutdownCtx, "http server stopping")
	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = srv.Close()
	}
	logger.InfoContext(shutdownCtx, "http server stopped")

	// 2. Scanner stops, then the workers drain what is already queued.
	logger.InfoContext(shutdownCtx, "background components stopping")
	backgroundErr := app.StopBackground(shutdownCtx)

	// 3. Flush telemetry before closing the pool: the final collection still
	// reads the active-trips gauge from the database.
	if err := otel.Shutdown(shutdownCtx); err != nil {
		logger.WarnContext(shutdownCtx, "telemetry flush failed", slog.Any("error", err))
	}

	// 4. Nobody uses the HTTP client and the database any more.
	app.Close()
	logger.InfoContext(shutdownCtx, "shutdown complete")

	if err := errors.Join(serveFailure, shutdownErr, backgroundErr); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}
