// Package secrets seals operator-supplied credentials at rest.
//
// A provider API key is written once through the settings page and read only
// by the process that calls the provider. No route returns it, so the
// ciphertext never leaves the server. Encrypting it means a database dump, a
// replica, or a backup tape is not a credential leak.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

// Box seals and opens short strings with AES-256-GCM.
type Box struct{ aead cipher.AEAD }

// NewBox derives the cipher from a 32-byte key supplied as base64 or hex.
// Anything else is refused at start-up rather than at the first write.
func NewBox(key string) (*Box, error) {
	raw, err := decodeKey(key)
	if err != nil {
		return nil, err
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("encryption key must decode to 32 bytes, got %d", len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("build cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("build gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal returns nonce || ciphertext. A fresh nonce per call is what keeps GCM
// safe, so it is generated here and never reused.
func (b *Box) Seal(plaintext string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (b *Box) Open(sealed []byte) (string, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return "", fmt.Errorf("sealed value is too short to contain a nonce")
	}
	out, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		// Almost always a rotated CONFIG_ENCRYPTION_KEY rather than tampering.
		return "", fmt.Errorf("open sealed value: %w", err)
	}
	return string(out), nil
}

// Hint is what the settings page shows in place of a key: enough to tell two
// keys apart, not enough to be one.
func Hint(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}

func decodeKey(key string) ([]byte, error) {
	if raw, err := base64.StdEncoding.DecodeString(key); err == nil && len(raw) == 32 {
		return raw, nil
	}
	if raw, err := hex.DecodeString(key); err == nil && len(raw) == 32 {
		return raw, nil
	}
	return nil, fmt.Errorf("encryption key must be 32 bytes encoded as base64 or hex")
}
