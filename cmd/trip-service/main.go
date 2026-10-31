package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Zuneve/template/internal/config"
	"github.com/Zuneve/template/internal/httpapi"
	"github.com/Zuneve/template/internal/postgres"
	"github.com/Zuneve/template/internal/repository"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalln(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, cfg.Database)
	if err != nil {
		log.Fatalln(err)
	}
	defer pool.Close()

	trips := repository.NewTripRepository(pool)
	history := repository.NewTripStatusHistoryRepository(pool)
	idempotency := repository.NewIdempotencyRepository(pool)
	txManager := postgres.NewTxManager(pool)

	apiImpl := httpapi.NewAPI(trips, history, idempotency, txManager, pool.Ping, cfg.Database.QueryTimeout)
	srv := httpapi.NewServer(cfg.HTTP.Addr, httpapi.NewRouter(apiImpl))

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalln(err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Println("graceful shutdown timed out, forcing close:", err)
		_ = srv.Close()
		os.Exit(1)
	}
}
