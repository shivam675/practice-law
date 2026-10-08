package auth

import (
	"bytes"
	"strings"
	"testing"
)

func TestRecoveryBoundary(t *testing.T) {
	for _, password := range []string{"short", strings.Repeat("x", 1025)} {
		if ValidateNewPassword(password) == nil {
			t.Fatal("accepted invalid password")
		}
	}
	if ValidateNewPassword(strings.Repeat("é", 10)) != nil {
		t.Fatal("rejected valid unicode password")
	}
	plain, digest, err := NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if !validResetToken(plain) || !bytes.Equal(digest, HashRefreshToken(plain)) || bytes.Equal(digest, []byte(plain)) {
		t.Fatal("unsafe token encoding")
	}
	for _, token := range []string{"", plain + "x", "!" + plain[1:]} {
		if validResetToken(token) {
			t.Fatal("accepted malformed token")
		}
	}
	tenant := Principal{permissions: map[string]struct{}{"user.edit": {}}}
	platform := Principal{permissions: map[string]struct{}{"user.edit": {}, "platform.organization.manage": {}}}
	if !canResetTarget(tenant, false) || canResetTarget(tenant, true) || !canResetTarget(platform, true) || canResetTarget(Principal{}, false) {
		t.Fatal("reset authorization boundary failed")
	}
}
