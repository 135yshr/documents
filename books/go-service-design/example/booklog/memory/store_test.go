package memory_test

import (
	"context"
	"errors"
	"testing"

	"example.com/booklog/domain"
	"example.com/booklog/memory"
	"example.com/booklog/usecase"
)

func TestCompletionCommitsReadingAndOutbox(t *testing.T) {
	r, _ := domain.NewReading("r1")
	s := memory.NewStore(r)
	u := usecase.CompleteReading{Store: s}
	if err := u.Execute(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	events, err := s.Pending(context.Background())
	if err != nil || len(events) != 1 || events[0].ID == "" || events[0].Type != "ReadingCompleted" {
		t.Fatalf("outbox: %+v, %v", events, err)
	}
	if err := u.Execute(context.Background(), "r1"); !errors.Is(err, domain.ErrAlreadyCompleted) {
		t.Fatalf("second completion: %v", err)
	}
	events, _ = s.Pending(context.Background())
	if len(events) != 1 {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestTransactionRollbackAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		r, _ := domain.NewReading("r1")
		s := memory.NewStore(r)
		ctx, stop := context.WithCancel(context.Background())
		failure := errors.New("failed after write")
		err := s.WithinTx(ctx, func(tx usecase.Tx) error {
			r, err := tx.Load(ctx, "r1")
			if err != nil {
				return err
			}
			_ = r.Complete()
			if err := tx.Save(ctx, r); err != nil {
				return err
			}
			if err := tx.AddEvent(ctx, usecase.Event{ID: "event"}); err != nil {
				return err
			}
			if cancel {
				stop()
				return nil
			}
			return failure
		})
		if cancel && !errors.Is(err, context.Canceled) || !cancel && !errors.Is(err, failure) {
			t.Fatalf("transaction error: %v", err)
		}
		stop()
		if err := s.WithinTx(context.Background(), func(tx usecase.Tx) error {
			got, err := tx.Load(context.Background(), "r1")
			if err != nil {
				return err
			}
			if got.Completed() {
				t.Error("rolled-back reading was completed")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		events, _ := s.Pending(context.Background())
		if len(events) != 0 {
			t.Fatalf("rolled-back events: %+v", events)
		}
	}
}

func TestCanceledRequestDoesNotCommit(t *testing.T) {
	r, _ := domain.NewReading("r1")
	s := memory.NewStore(r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (usecase.CompleteReading{Store: s}).Execute(ctx, "r1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	events, _ := s.Pending(context.Background())
	if len(events) != 0 {
		t.Fatal("canceled request created an event")
	}
}
