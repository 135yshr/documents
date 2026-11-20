package domain

import "errors"

var (
	ErrInvalidID        = errors.New("reading ID is required")
	ErrAlreadyCompleted = errors.New("reading already completed")
)

type Reading struct {
	id        string
	completed bool
}

func NewReading(id string) (Reading, error) {
	if id == "" {
		return Reading{}, ErrInvalidID
	}
	return Reading{id: id}, nil
}

func (r Reading) ID() string      { return r.id }
func (r Reading) Completed() bool { return r.completed }

func (r *Reading) Complete() error {
	if r.completed {
		return ErrAlreadyCompleted
	}
	r.completed = true
	return nil
}
