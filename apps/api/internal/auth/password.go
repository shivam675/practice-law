package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters. Tuned for roughly 50 ms on a modern server core, which
// is the usual balance between login latency and offline cracking cost.
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 64 * 1024 // 64 MiB
	argonThreads uint8  = 2
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

var ErrPasswordMismatch = errors.New("password does not match")

// HashPassword returns a PHC-formatted argon2id hash. The parameters are
// encoded in the string so they can be raised later without invalidating
// existing hashes.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches encoded. It also reports
// whether the hash used weaker parameters than current policy, so the caller
// can transparently rehash on a successful login.
func VerifyPassword(encoded, password string) (needsRehash bool, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("unsupported password hash format")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("parse hash version: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version %d", version)
	}

	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, fmt.Errorf("parse hash parameters: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode hash: %w", err)
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, ErrPasswordMismatch
	}

	needsRehash = memory < argonMemory || time < argonTime || threads < argonThreads
	return needsRehash, nil
}

// dummyHash is verified against when an email does not exist, so a missing
// account and a wrong password take the same amount of time.
var dummyHash string

func init() {
	h, err := HashPassword("megamoot-timing-equaliser")
	if err != nil {
		panic("auth: cannot initialise dummy password hash: " + err.Error())
	}
	dummyHash = h
}
