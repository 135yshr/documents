package web_test

import (
	"context"
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

type recordingCompleter struct{ ids []string }

func (c *recordingCompleter) Execute(_ context.Context, id string) error {
	c.ids = append(c.ids, id)
	return nil
}

// ID の部分がないパスでは、ユースケースを呼ばずに 404 を返します。
// "/readings/complete" は接頭辞 "/readings/" と接尾辞 "/complete" が重なるため、
// ID "complete" の読了として扱ってはいけません。
func TestCompleteHTTPRoutesOnlyPathsWithID(t *testing.T) {
	for _, tc := range []struct {
		path   string
		status int
		wantID string // 空ならユースケースを呼ばないことを期待します
	}{
		{"/readings/complete", http.StatusNotFound, ""},
		{"/readings//complete", http.StatusNotFound, ""},
		{"/readings/a/b/complete", http.StatusNotFound, ""},
		{"/readings/demo", http.StatusNotFound, ""},
		{"/readings/complete/complete", http.StatusNoContent, "complete"},
		{"/readings/demo/complete", http.StatusNoContent, "demo"},
	} {
		completer := &recordingCompleter{}
		rec := httptest.NewRecorder()
		web.Handler(completer).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tc.path, nil))
		if rec.Code != tc.status {
			t.Errorf("POST %s: got %d, want %d", tc.path, rec.Code, tc.status)
		}
		switch {
		case tc.wantID == "" && len(completer.ids) != 0:
			t.Errorf("POST %s: usecase called with %v, want no call", tc.path, completer.ids)
		case tc.wantID != "" && (len(completer.ids) != 1 || completer.ids[0] != tc.wantID):
			t.Errorf("POST %s: usecase called with %v, want [%s]", tc.path, completer.ids, tc.wantID)
		}
	}
}
