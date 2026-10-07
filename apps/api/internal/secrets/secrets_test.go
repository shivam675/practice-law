package secrets

import (
	"encoding/hex"
	"strings"
	"testing"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // 32 bytes hex

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := NewBox(testKey)
	if err != nil {
		t.Fatalf("new box: %v", err)
	}

	const token = "sk-remote-ollama-abcdef123456"
	sealed, err := box.Seal(token)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(string(sealed), token) {
		t.Fatal("plaintext survived into the ciphertext")
	}

	got, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got != token {
		t.Fatalf("round trip gave %q, want %q", got, token)
	}
}

func TestSealUsesAFreshNonce(t *testing.T) {
	box, _ := NewBox(testKey)
	a, _ := box.Seal("same")
	b, _ := box.Seal("same")
	if string(a) == string(b) {
		t.Fatal("two seals of the same value are identical; the nonce is being reused")
	}
}

func TestOpenRejectsAnotherKey(t *testing.T) {
	box, _ := NewBox(testKey)
	sealed, _ := box.Seal("secret")

	other, err := NewBox(hex.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("new box: %v", err)
	}
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("a different key opened the ciphertext")
	}
}

func TestNewBoxRejectsShortKey(t *testing.T) {
	if _, err := NewBox("too-short"); err == nil {
		t.Fatal("a short key was accepted")
	}
}

func TestHintKeepsOnlyTheTail(t *testing.T) {
	if got := Hint("sk-abcdef123456"); got != "****3456" {
		t.Fatalf("hint = %q", got)
	}
	if got := Hint("ab"); got != "****" {
		t.Fatalf("short hint = %q", got)
	}
}
