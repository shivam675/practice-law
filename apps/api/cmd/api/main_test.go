package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type readinessStub struct{ err error }

func (s readinessStub) Ping(context.Context) error    { return s.err }
func (s readinessStub) Healthy(context.Context) error { return s.err }

func TestReadinessRequiresExtraction(t *testing.T) {
	_, ready := healthHandlers(readinessStub{}, readinessStub{err: errors.New("down")})
	w := httptest.NewRecorder()
	ready(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", w.Code)
	}
}
