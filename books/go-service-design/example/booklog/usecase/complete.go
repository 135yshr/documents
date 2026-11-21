package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"example.com/booklog/domain"
)

var ErrNotFound = errors.New("reading not found")

type Event struct {
	ID        string
	ReadingID string
	Type      string
}

type Tx interface {
	Load(context.Context, string) (domain.Reading, error)
	Save(context.Context, domain.Reading) error
	AddEvent(context.Context, Event) error
}

type Store interface {
	WithinTx(context.Context, func(Tx) error) error
}

type CompleteReading struct {
	Store Store
}

func (u CompleteReading) Execute(ctx context.Context, id string) error {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	event := Event{ID: hex.EncodeToString(raw[:]), ReadingID: id, Type: "ReadingCompleted"}
	return u.Store.WithinTx(ctx, func(tx Tx) error {
		reading, err := tx.Load(ctx, id)
		if err != nil {
			return err
		}
		if err := reading.Complete(); err != nil {
			return err
		}
		if err := tx.Save(ctx, reading); err != nil {
			return err
		}
		return tx.AddEvent(ctx, event)
	})
}
