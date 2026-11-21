package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
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

// 終了時は、進行中の HTTP 処理を待ってから worker を止めます。
// 処理中のリクエストがある間に worker が動き続けることを、Outbox の呼び出しで確かめます。
func TestShutdownDrainsHTTPBeforeStoppingWorker(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{}, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker := delivery.Worker{Outbox: observedOutbox{called}, Consumer: delivery.NewMemoryConsumer(), Logger: logger, Interval: 5 * time.Millisecond}

	inHandler := make(chan struct{})
	releaseHandler := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseHandler) })
	t.Cleanup(release) // 途中で失敗しても、待機中のハンドラを残しません。
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(inHandler)
		<-releaseHandler
		w.WriteHeader(http.StatusNoContent)
	})
	server := &http.Server{Addr: addr, Handler: mux}
	done := make(chan error, 1)
	go func() { done <- run(ctx, server, worker, logger) }()

	// サーバーの起動を待ちながら、ハンドラで止まるリクエストを1件送ります。
	respDone := make(chan error, 1)
	go func() {
		var lastErr error
		for range 200 {
			resp, err := http.Get("http://" + addr + "/slow")
			if err != nil {
				lastErr = err
				time.Sleep(10 * time.Millisecond)
				continue
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				respDone <- fmt.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
				return
			}
			respDone <- nil
			return
		}
		respDone <- lastErr
	}()
	select {
	case <-inHandler:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach the handler")
	}

	cancel()
	// Shutdown が受付を止めるまで待ちます。これ以降は drain 中です。
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("server kept accepting connections after cancel")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// drain 中も worker が Outbox を呼び続けることを確かめます。
	select {
	case <-called:
	default:
	}
	for i := range 2 {
		select {
		case <-called:
		case err := <-done:
			t.Fatalf("run returned while a request was in progress: %v", err)
		case <-time.After(time.Second):
			t.Fatalf("worker stopped before HTTP drain finished (tick %d)", i)
		}
	}

	release()
	select {
	case err := <-respDone:
		if err != nil {
			t.Fatalf("in-flight request failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request did not finish")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}
