// Package config loads runtime configuration from the environment.
// Every value has a development default except secrets, which must be set
// explicitly outside development.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env      string
	LogLevel string

	HTTPAddr  string
	PublicURL string
	WebURL    string
	CORSOrigins []string

	DatabaseURL string
	RedisURL    string

	JWTSigningKey   []byte
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	CookieDomain    string
	CookieSecure    bool

	S3Endpoint      string
	S3Region        string
	S3Bucket        string
	S3AccessKey     string
	S3SecretKey     string
	S3ForcePathStyle bool

	AIServiceURL    string
	MediaServiceURL string
	MediaPublicWSURL string

	MaxConcurrentLiveSessions int

	SeedOrgName       string
	SeedAdminEmail    string
	SeedAdminPassword string
}

func (c Config) IsProduction() bool { return c.Env == "production" }

// Load reads the environment and validates anything that would fail later at a
// worse time.
func Load() (Config, error) {
	c := Config{
		Env:      env("APP_ENV", "development"),
		LogLevel: env("LOG_LEVEL", "info"),

		HTTPAddr:    env("API_HTTP_ADDR", ":8080"),
		PublicURL:   env("API_PUBLIC_URL", "http://localhost:8080"),
		WebURL:      env("WEB_PUBLIC_URL", "http://localhost:5173"),
		CORSOrigins: splitCSV(env("CORS_ALLOWED_ORIGINS", "http://localhost:5173")),

		DatabaseURL: env("DATABASE_URL", ""),
		RedisURL:    env("REDIS_URL", "redis://redis:6379/0"),

		CookieDomain: env("COOKIE_DOMAIN", "localhost"),

		S3Endpoint:  env("S3_ENDPOINT", "http://minio:9000"),
		S3Region:    env("S3_REGION", "us-east-1"),
		S3Bucket:    env("S3_BUCKET", "megamoot"),
		S3AccessKey: env("S3_ACCESS_KEY", ""),
		S3SecretKey: env("S3_SECRET_KEY", ""),

		AIServiceURL:     env("AI_SERVICE_URL", "http://ai:8100"),
		MediaServiceURL:  env("MEDIA_SERVICE_URL", "http://media:8200"),
		MediaPublicWSURL: env("MEDIA_PUBLIC_WS_URL", "ws://localhost:8200"),

		SeedOrgName:       env("SEED_ORG_NAME", "Demo Law School"),
		SeedAdminEmail:    env("SEED_ADMIN_EMAIL", ""),
		SeedAdminPassword: env("SEED_ADMIN_PASSWORD", ""),
	}

	if c.DatabaseURL == "" {
		c.DatabaseURL = buildDatabaseURL()
	}

	c.CookieSecure = envBool("COOKIE_SECURE", false)
	c.S3ForcePathStyle = envBool("S3_FORCE_PATH_STYLE", true)
	c.MaxConcurrentLiveSessions = envInt("MAX_CONCURRENT_LIVE_SESSIONS", 4)

	var err error
	if c.AccessTokenTTL, err = envDuration("ACCESS_TOKEN_TTL", 10*time.Minute); err != nil {
		return c, err
	}
	if c.RefreshTokenTTL, err = envDuration("REFRESH_TOKEN_TTL", 720*time.Hour); err != nil {
		return c, err
	}

	key := env("JWT_SIGNING_KEY", "")
	if key == "" {
		return c, fmt.Errorf("JWT_SIGNING_KEY is required")
	}
	if len(key) < 32 {
		return c, fmt.Errorf("JWT_SIGNING_KEY must be at least 32 characters, got %d", len(key))
	}
	if c.IsProduction() && strings.Contains(key, "CHANGE_ME") {
		return c, fmt.Errorf("JWT_SIGNING_KEY still holds the development placeholder")
	}
	c.JWTSigningKey = []byte(key)

	if c.IsProduction() && !c.CookieSecure {
		return c, fmt.Errorf("COOKIE_SECURE must be true in production")
	}

	return c, nil
}

func buildDatabaseURL() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		env("POSTGRES_USER", "megamoot"),
		env("POSTGRES_PASSWORD", "megamoot_dev_password"),
		env("POSTGRES_HOST", "localhost"),
		env("POSTGRES_PORT", "5432"),
		env("POSTGRES_DB", "megamoot"),
		env("POSTGRES_SSLMODE", "disable"),
	)
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
