package delivery

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"example.com/booklog/usecase"
)

type Outbox interface {
	Pending(context.Context) ([]usecase.Event, error)
	MarkDelivered(context.Context, string) error
}

type Consumer interface {
	Consume(context.Context, usecase.Event) error
}

type Worker struct {
	Outbox   Outbox
	Consumer Consumer
	Logger   *slog.Logger
	Interval time.Duration
}

func (w Worker) Dispatch(ctx context.Context) error {
	events, err := w.Outbox.Pending(ctx)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := w.Consumer.Consume(ctx, event); err != nil {
			return err
		}
		if err := w.Outbox.MarkDelivered(ctx, event.ID); err != nil {
			return err
		}
	}
	return nil
}

func (w Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		if err := w.Dispatch(ctx); err != nil && ctx.Err() == nil {
			w.Logger.ErrorContext(ctx, "outbox dispatch failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// MemoryConsumer applies its effect and records the event ID under one lock.
type MemoryConsumer struct {
	mu      sync.Mutex
	seen    map[string]bool
	applied int
}

func NewMemoryConsumer() *MemoryConsumer {
	return &MemoryConsumer{seen: make(map[string]bool)}
}

func (c *MemoryConsumer) Consume(ctx context.Context, event usecase.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.seen[event.ID] {
		c.applied++
		c.seen[event.ID] = true
	}
	return nil
}

func (c *MemoryConsumer) Applied() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.applied
}
