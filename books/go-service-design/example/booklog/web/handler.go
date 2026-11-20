package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"example.com/booklog/domain"
	"example.com/booklog/usecase"
)

type Completer interface {
	Execute(context.Context, string) error
}

func Handler(completer Completer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/readings/"), "/complete")
		if !strings.HasPrefix(r.URL.Path, "/readings/") || !strings.HasSuffix(r.URL.Path, "/complete") || id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		if err := completer.Execute(r.Context(), id); err != nil {
			switch {
			case errors.Is(err, usecase.ErrNotFound):
				http.Error(w, "reading not found", http.StatusNotFound)
			case errors.Is(err, domain.ErrAlreadyCompleted):
				http.Error(w, "reading already completed", http.StatusConflict)
			default:
				http.Error(w, "request failed", http.StatusInternalServerError)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
