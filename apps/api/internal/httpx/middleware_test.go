package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestAccessLogPreservesResponseController(t *testing.T) {
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
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

func TestWriteDeadline(t *testing.T) {
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	before := time.Now().Add(2 * time.Minute)
	WriteDeadline(2*time.Minute)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/upload", nil))
	if w.deadline.Before(before) || w.deadline.After(time.Now().Add(2*time.Minute+time.Second)) {
		t.Fatalf("write deadline = %s", w.deadline)
	}
}
