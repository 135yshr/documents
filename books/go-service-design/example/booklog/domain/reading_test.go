package domain

import (
	"errors"
	"testing"
)

func TestReadingCompletion(t *testing.T) {
	if _, err := NewReading(""); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("empty ID: %v", err)
	}
	r, err := NewReading("r1")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(); err != nil || !r.Completed() {
		t.Fatalf("complete: %v", err)
	}
	if err := r.Complete(); !errors.Is(err, ErrAlreadyCompleted) {
		t.Fatalf("second completion: %v", err)
	}
}
