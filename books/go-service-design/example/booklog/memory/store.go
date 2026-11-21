package memory

import (
	"context"
	"sync"

	"example.com/booklog/domain"
	"example.com/booklog/usecase"
)

// Store demonstrates atomic commits, but loses all data on process exit.
type Store struct {
	mu       sync.Mutex
	readings map[string]domain.Reading
	outbox   map[string]usecase.Event
}

func NewStore(readings ...domain.Reading) *Store {
	s := &Store{readings: make(map[string]domain.Reading), outbox: make(map[string]usecase.Event)}
	for _, r := range readings {
		s.readings[r.ID()] = r
	}
	return s
}

type tx struct {
	readings map[string]domain.Reading
	outbox   map[string]usecase.Event
}

func (s *Store) WithinTx(ctx context.Context, fn func(usecase.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	t := &tx{readings: make(map[string]domain.Reading, len(s.readings)), outbox: make(map[string]usecase.Event, len(s.outbox))}
	for k, v := range s.readings {
		t.readings[k] = v
	}
	for k, v := range s.outbox {
		t.outbox[k] = v
	}
	if err := fn(t); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.readings, s.outbox = t.readings, t.outbox
	return nil
}

func (t *tx) Load(ctx context.Context, id string) (domain.Reading, error) {
	if err := ctx.Err(); err != nil {
		return domain.Reading{}, err
	}
	r, ok := t.readings[id]
	if !ok {
		return domain.Reading{}, usecase.ErrNotFound
	}
	return r, nil
}

func (t *tx) Save(ctx context.Context, r domain.Reading) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.readings[r.ID()] = r
	return nil
}

func (t *tx) AddEvent(ctx context.Context, event usecase.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.outbox[event.ID] = event
	return nil
}

func (s *Store) Pending(ctx context.Context) ([]usecase.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]usecase.Event, 0, len(s.outbox))
	for _, e := range s.outbox {
		events = append(events, e)
	}
	return events, ctx.Err()
}

func (s *Store) MarkDelivered(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(s.outbox, id)
	return nil
}
