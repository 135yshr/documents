package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/booklog/delivery"
	"example.com/booklog/domain"
	"example.com/booklog/memory"
	"example.com/booklog/usecase"
	"example.com/booklog/web"
)

func run(ctx context.Context, server *http.Server, worker delivery.Worker, logger *slog.Logger) error {
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		worker.Run(workerCtx)
	}()
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	var result error
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP shutdown failed", "error", err)
		_ = server.Close()
		result = errors.Join(result, err)
	}
	stopWorker()
	select {
	case <-workerDone:
	case <-shutdownCtx.Done():
		result = errors.Join(result, shutdownCtx.Err())
	}
	return result
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	reading, err := domain.NewReading("demo")
	if err != nil {
		logger.Error("create reading", "error", err)
		os.Exit(1)
	}
	store := memory.NewStore(reading)
	worker := delivery.Worker{Outbox: store, Consumer: delivery.NewMemoryConsumer(), Logger: logger, Interval: time.Second}
	server := &http.Server{Addr: ":8080", Handler: web.Handler(usecase.CompleteReading{Store: store}), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("listening", "address", server.Addr)
	if err := run(ctx, server, worker, logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
