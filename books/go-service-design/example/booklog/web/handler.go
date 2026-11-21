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
		// 接頭辞を外した残りから接尾辞を外します。元のパスに対して両方を別々に調べると、
		// "/readings/complete" のように接頭辞と接尾辞が重なるパスを ID "complete" として受け入れてしまいます。
		rest, ok := strings.CutPrefix(r.URL.Path, "/readings/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		id, ok := strings.CutSuffix(rest, "/complete")
		if !ok || id == "" || strings.Contains(id, "/") {
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
