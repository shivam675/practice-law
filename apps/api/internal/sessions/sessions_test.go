package sessions

import (
	"net/http/httptest"
	"testing"
)

func TestRequestIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	if got, err := requestIP(r); err != nil || got != "192.0.2.1" {
		t.Fatalf("remote address = %q, %v", got, err)
	}
	r.Header.Set("X-Forwarded-For", "198.51.100.8, 192.0.2.1")
	if got, err := requestIP(r); err != nil || got != "198.51.100.8" {
		t.Fatalf("forwarded address = %q, %v", got, err)
	}
}
