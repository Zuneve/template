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

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()
	logger.InfoContext(ctx, "http server started",
		slog.String("addr", cfg.HTTP.Addr),
		slog.String("version", version),
	)

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	logger.InfoContext(shutdownCtx, "http server stopping")
	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = srv.Close()
	}

	// Flush telemetry before closing the pool: the final collection still
	// reads the active-trips gauge from the database.
	if err := otel.Shutdown(shutdownCtx); err != nil {
		logger.WarnContext(shutdownCtx, "telemetry flush failed", slog.Any("error", err))
	}
	app.Close()
	logger.InfoContext(shutdownCtx, "http server stopped")

	if shutdownErr != nil {
		return fmt.Errorf("graceful shutdown: %w", shutdownErr)
	}
	return nil
}
