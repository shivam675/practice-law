package config

import (
	"strings"
	"testing"
)

func TestProductionRefusesDevelopmentDatabasePassword(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_SIGNING_KEY", strings.Repeat("k", 40))
	t.Setenv("CONFIG_ENCRYPTION_KEY", strings.Repeat("ab", 32))
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("MEDIA_SERVICE_TOKEN", strings.Repeat("m", 40))
	t.Setenv("AI_SERVICE_TOKEN", strings.Repeat("a", 40))
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_PASSWORD", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "database password") {
		t.Fatalf("development database password accepted: %v", err)
	}
	t.Setenv("POSTGRES_PASSWORD", strings.Repeat("p", 40))
	if _, err := Load(); err != nil {
		t.Fatalf("valid production config refused: %v", err)
	}
	t.Setenv("AI_SERVICE_TOKEN", "short")
	if _, err := Load(); err == nil {
		t.Fatal("short AI service token accepted")
	}
}
