package delivery

import (
	"context"
	"errors"
	"testing"

	"example.com/booklog/domain"
	"example.com/booklog/memory"
	"example.com/booklog/usecase"
)

type failOnce struct {
	*memory.Store
	failed bool
}

func (s *failOnce) MarkDelivered(ctx context.Context, id string) error {
	if !s.failed {
		s.failed = true
		return errors.New("ack lost")
	}
	return s.Store.MarkDelivered(ctx, id)
}

func TestRedeliveryIsIdempotent(t *testing.T) {
	r, _ := domain.NewReading("r1")
	s := memory.NewStore(r)
	if err := (usecase.CompleteReading{Store: s}).Execute(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	c := NewMemoryConsumer()
	w := Worker{Outbox: &failOnce{Store: s}, Consumer: c}
	if err := w.Dispatch(context.Background()); err == nil {
		t.Fatal("expected lost acknowledgement")
	}
	if err := w.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Applied() != 1 {
		t.Fatalf("applied %d times", c.Applied())
	}
	pending, _ := s.Pending(context.Background())
	if len(pending) != 0 {
		t.Fatalf("pending: %+v", pending)
	}
}
