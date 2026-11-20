package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"example.com/booklog/delivery"
	"example.com/booklog/usecase"
)

type observedOutbox struct{ called chan struct{} }

func (o observedOutbox) Pending(ctx context.Context) ([]usecase.Event, error) {
	select {
	case o.called <- struct{}{}:
	default:
	}
	return nil, ctx.Err()
}
func (observedOutbox) MarkDelivered(context.Context, string) error { return nil }

func TestShutdownStopsWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{}, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker := delivery.Worker{Outbox: observedOutbox{called}, Consumer: delivery.NewMemoryConsumer(), Logger: logger, Interval: time.Hour}
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.NewServeMux()}
	done := make(chan error, 1)
	go func() { done <- run(ctx, server, worker, logger) }()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}
