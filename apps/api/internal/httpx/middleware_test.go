package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineWriter) SetWriteDeadline(t time.Time) error { w.deadline = t; return nil }
func TestAccessLogPreservesResponseController(t *testing.T) {
	w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	deadline := time.Now().Add(time.Minute)
	AccessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(deadline); err != nil {
			t.Fatal(err)
		}
	})).ServeHTTP(w, httptest.NewRequest("POST", "/regrade", nil))
	if !w.deadline.Equal(deadline) {
		t.Fatal("response deadline was not forwarded")
	}
}
