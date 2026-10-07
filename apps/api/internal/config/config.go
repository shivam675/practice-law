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

	HTTPAddr    string
	PublicURL   string
	WebURL      string
	CORSOrigins []string

	DatabaseURL string
	RedisURL    string

	JWTSigningKey   []byte
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	CookieDomain    string
	CookieSecure    bool

	// Seals provider credentials in model_providers. Separate from the JWT
	// key so that rotating one does not invalidate the other.
	ConfigEncryptionKey string

	BlobDriver       string
	BlobFSRoot       string
	S3Endpoint       string
	S3Region         string
	S3Bucket         string
	S3AccessKey      string
	S3SecretKey      string
	S3ForcePathStyle bool

	AIServiceURL      string
	AIServiceToken    string
	MediaServiceURL   string
	MediaServiceToken string
	MediaPublicWSURL  string

	MaxConcurrentLiveSessions int

	SeedDemoPassword       string
	SeedSuperAdminEmail    string
	SeedSuperAdminPassword string

	SeedOrgName       string
	SeedAdminEmail    string
	SeedAdminPassword string
}

// devEncryptionKey is the value shipped in .env.example. Production refuses it.
const devEncryptionKey = "ZGV2ZWxvcG1lbnQtb25seS1rZXktZG8tbm90LXVzZSE="

func (c Config) IsProduction() bool { return c.Env == "production" }

// Load reads the environment and validates anything that would fail later at a
// worse time.
func Load() (Config, error) {
	c := Config{
		Env:               env("APP_ENV", "development"),
		MediaServiceToken: env("MEDIA_SERVICE_TOKEN", "local_speech_development_only_change_me"),
		LogLevel:          env("LOG_LEVEL", "info"),

		HTTPAddr:    env("API_HTTP_ADDR", ":8080"),
		PublicURL:   env("API_PUBLIC_URL", "http://localhost:8080"),
		WebURL:      env("WEB_PUBLIC_URL", "http://localhost:5173"),
		CORSOrigins: splitCSV(env("CORS_ALLOWED_ORIGINS", "http://localhost:5173")),

		DatabaseURL: env("DATABASE_URL", ""),
		RedisURL:    env("REDIS_URL", "redis://redis:6379/0"),

		CookieDomain: env("COOKIE_DOMAIN", "localhost"),

		ConfigEncryptionKey: env("CONFIG_ENCRYPTION_KEY", ""),

		BlobDriver:  env("BLOB_DRIVER", "filesystem"),
		BlobFSRoot:  env("BLOB_FS_ROOT", "/var/lib/megamoot/blobs"),
		S3Endpoint:  env("S3_ENDPOINT", ""),
		S3Region:    env("S3_REGION", "us-east-1"),
		S3Bucket:    env("S3_BUCKET", "megamoot"),
		S3AccessKey: env("S3_ACCESS_KEY", ""),
		S3SecretKey: env("S3_SECRET_KEY", ""),

		AIServiceURL:     env("AI_SERVICE_URL", "http://ai:8100"),
		AIServiceToken:   env("AI_SERVICE_TOKEN", ""),
		MediaServiceURL:  env("MEDIA_SERVICE_URL", "http://media:8200"),
		MediaPublicWSURL: env("MEDIA_PUBLIC_WS_URL", "ws://localhost:8200"),

		SeedDemoPassword:       env("SEED_DEMO_PASSWORD", ""),
		SeedSuperAdminEmail:    env("SEED_SUPERADMIN_EMAIL", ""),
		SeedSuperAdminPassword: env("SEED_SUPERADMIN_PASSWORD", ""),

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

	if c.ConfigEncryptionKey == "" {
		return c, fmt.Errorf("CONFIG_ENCRYPTION_KEY is required; " +
			"it seals model provider credentials at rest. Generate: openssl rand -base64 32")
	}
	// The placeholder is a real 32-byte key, because an unusable placeholder
	// would only fail at the first write. It is refused by name instead.
	if c.IsProduction() && c.ConfigEncryptionKey == devEncryptionKey {
		return c, fmt.Errorf("CONFIG_ENCRYPTION_KEY still holds the development placeholder")
	}

	if c.IsProduction() && !c.CookieSecure {
		return c, fmt.Errorf("COOKIE_SECURE must be true in production")
	}
	if len(c.MediaServiceToken) < 32 || (c.IsProduction() && strings.Contains(c.MediaServiceToken, "change_me")) {
		return c, fmt.Errorf("MEDIA_SERVICE_TOKEN must contain at least 32 characters and must not use the development value in production")
	}

	// Shared demo credentials are a development convenience. In production
	// they would be a published set of working logins.
	if c.IsProduction() && c.SeedDemoPassword != "" {
		return c, fmt.Errorf("SEED_DEMO_PASSWORD must be unset in production")
	}

	// The seeded super administrator is a development convenience. Shipping
	// its default credentials to production would hand over the whole platform.
	if c.IsProduction() && c.SeedSuperAdminPassword != "" {
		if len(c.SeedSuperAdminPassword) < 16 {
			return c, fmt.Errorf("SEED_SUPERADMIN_PASSWORD is too weak for production; " +
				"use at least 16 characters or leave it unset")
		}
	}

	if c.AIServiceToken == "" {
		return c, fmt.Errorf("AI_SERVICE_TOKEN is required; it authenticates the control plane to the AI plane")
	}
	if c.IsProduction() && strings.Contains(c.AIServiceToken, "change_me") {
		return c, fmt.Errorf("AI_SERVICE_TOKEN still holds the development placeholder")
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
