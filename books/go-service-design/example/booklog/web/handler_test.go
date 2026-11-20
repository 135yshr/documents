package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/booklog/domain"
	"example.com/booklog/memory"
	"example.com/booklog/usecase"
	"example.com/booklog/web"
)

func TestCompleteHTTP(t *testing.T) {
	r, _ := domain.NewReading("demo")
	handler := web.Handler(usecase.CompleteReading{Store: memory.NewStore(r)})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/readings/demo/complete", http.StatusMethodNotAllowed},
		{"POST", "/other", http.StatusNotFound},
		{"POST", "/readings/missing/complete", http.StatusNotFound},
		{"POST", "/readings/demo/complete", http.StatusNoContent},
		{"POST", "/readings/demo/complete", http.StatusConflict},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.status {
			t.Errorf("%s %s: got %d, want %d", tc.method, tc.path, rec.Code, tc.status)
		}
	}
}
