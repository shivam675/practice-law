// Package platformcfg owns model provider and tier configuration.
//
// This is the one part of the system that is deliberately not tenant scoped.
// There is one box, one set of providers, and which model answers a given
// tier is an operator decision. Every route here is gated on
// platform.model.configure, the platform permission that no organisation role
// can be granted.
//
// A provider credential is written here and never read back out. The only
// code that sees the plaintext is the client that calls the provider.
package platformcfg

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/intelimek/megamoot/apps/api/internal/llm"
	"github.com/intelimek/megamoot/apps/api/internal/secrets"
)

// Tiers is the closed set a binding may target. Adding one is a platform
// decision, the same way adding a stage kind is.
var Tiers = []string{"monitor", "judge", "grader", "embedding"}

type Provider struct {
	ID         uuid.UUID `json:"id"`
	Key        string    `json:"key"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	BaseURL    string    `json:"base_url"`
	APIKeyHint string    `json:"api_key_hint"`
	HasAPIKey  bool      `json:"has_api_key"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Binding struct {
	Tier        string    `json:"tier"`
	ProviderID  uuid.UUID `json:"provider_id"`
	ProviderKey string    `json:"provider_key"`
	Model       string    `json:"model"`
	Temperature float64   `json:"temperature"`
	TopP        float64   `json:"top_p"`
	MaxTokens   int       `json:"max_tokens"`
	TimeoutMS   int       `json:"timeout_ms"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Store struct {
	pool *pgxpool.Pool
	box  *secrets.Box
}

func NewStore(pool *pgxpool.Pool, box *secrets.Box) *Store {
	return &Store{pool: pool, box: box}
}

/* ---------------------------------------------------------------- providers */

func (s *Store) ListProviders(ctx context.Context) ([]Provider, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, key, name, kind, base_url, api_key_hint,
		       api_key_cipher IS NOT NULL, is_active, created_at, updated_at
		FROM model_providers
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	out := []Provider{}
	for rows.Next() {
		var p Provider
		if err := rows.Scan(&p.ID, &p.Key, &p.Name, &p.Kind, &p.BaseURL,
			&p.APIKeyHint, &p.HasAPIKey, &p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type ProviderInput struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

func (in ProviderInput) validate() error {
	var problems []string
	if !keyPattern(in.Key) {
		problems = append(problems, "key must be lowercase letters, digits and underscores")
	}
	if strings.TrimSpace(in.Name) == "" {
		problems = append(problems, "name is required")
	}
	if in.Kind != "openai_compatible" && in.Kind != "anthropic" {
		problems = append(problems, "kind must be openai_compatible or anthropic")
	}
	problems = append(problems, baseURLProblems(in.BaseURL)...)

	if len(problems) > 0 {
		return invalid("invalid_provider", "The provider is not valid.", problems)
	}
	return nil
}

func (s *Store) CreateProvider(ctx context.Context, actorID uuid.UUID, in ProviderInput) (Provider, error) {
	if err := in.validate(); err != nil {
		return Provider{}, err
	}

	cipher, hint, err := s.sealKey(in.APIKey)
	if err != nil {
		return Provider{}, err
	}

	var p Provider
	err = s.pool.QueryRow(ctx, `
		INSERT INTO model_providers (key, name, kind, base_url, api_key_cipher, api_key_hint, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, key, name, kind, base_url, api_key_hint,
		          api_key_cipher IS NOT NULL, is_active, created_at, updated_at`,
		in.Key, strings.TrimSpace(in.Name), in.Kind, strings.TrimSpace(in.BaseURL),
		cipher, hint, actorID,
	).Scan(&p.ID, &p.Key, &p.Name, &p.Kind, &p.BaseURL, &p.APIKeyHint,
		&p.HasAPIKey, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return Provider{}, httpx.ErrConflict("A provider with that key already exists.")
		}
		return Provider{}, fmt.Errorf("create provider: %w", err)
	}
	return p, nil
}

// ProviderPatch leaves every absent field alone. An api_key of "" clears the
// stored credential; omitting it keeps whatever is there, which is what an
// operator editing only the base URL expects.
type ProviderPatch struct {
	Name     *string `json:"name"`
	BaseURL  *string `json:"base_url"`
	APIKey   *string `json:"api_key"`
	IsActive *bool   `json:"is_active"`
}

func (s *Store) UpdateProvider(ctx context.Context, id uuid.UUID, in ProviderPatch) (Provider, error) {
	var problems []string
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		problems = append(problems, "name cannot be empty")
	}
	if in.BaseURL != nil {
		problems = append(problems, baseURLProblems(*in.BaseURL)...)
	}
	if len(problems) > 0 {
		return Provider{}, invalid("invalid_provider", "The provider is not valid.", problems)
	}

	var cipher []byte
	var hint string
	var setKey bool
	if in.APIKey != nil {
		var err error
		if cipher, hint, err = s.sealKey(*in.APIKey); err != nil {
			return Provider{}, err
		}
		setKey = true
	}

	var p Provider
	err := s.pool.QueryRow(ctx, `
		UPDATE model_providers SET
			name           = COALESCE($2, name),
			base_url       = COALESCE($3, base_url),
			is_active      = COALESCE($4, is_active),
			api_key_cipher = CASE WHEN $5 THEN $6 ELSE api_key_cipher END,
			api_key_hint   = CASE WHEN $5 THEN $7 ELSE api_key_hint END
		WHERE id = $1
		RETURNING id, key, name, kind, base_url, api_key_hint,
		          api_key_cipher IS NOT NULL, is_active, created_at, updated_at`,
		id, trimmed(in.Name), trimmed(in.BaseURL), in.IsActive, setKey, cipher, hint,
	).Scan(&p.ID, &p.Key, &p.Name, &p.Kind, &p.BaseURL, &p.APIKeyHint,
		&p.HasAPIKey, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Provider{}, httpx.ErrNotFound()
	}
	if err != nil {
		return Provider{}, fmt.Errorf("update provider: %w", err)
	}
	return p, nil
}

func (s *Store) DeleteProvider(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM model_providers WHERE id = $1`, id)
	if isForeignKeyViolation(err) {
		return httpx.ErrConflict(
			"That provider is still bound to a tier. Point the tier somewhere else first.")
	}
	if err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound()
	}
	return nil
}

// credentials decrypts one provider for a call. The plaintext exists only for
// the lifetime of the request that needs it.
func (s *Store) credentials(ctx context.Context, id uuid.UUID) (llm.Provider, error) {
	var kind, baseURL string
	var cipher []byte
	err := s.pool.QueryRow(ctx, `
		SELECT kind, base_url, api_key_cipher
		FROM model_providers WHERE id = $1 AND is_active`, id).
		Scan(&kind, &baseURL, &cipher)
	if errors.Is(err, pgx.ErrNoRows) {
		return llm.Provider{}, httpx.ErrBadRequest("That provider does not exist or is disabled.")
	}
	if err != nil {
		return llm.Provider{}, fmt.Errorf("load provider credentials: %w", err)
	}

	p := llm.Provider{Kind: kind, BaseURL: baseURL}
	if len(cipher) > 0 {
		plain, err := s.box.Open(cipher)
		if err != nil {
			// Rotating CONFIG_ENCRYPTION_KEY without re-entering the keys
			// lands here. Say so, rather than reporting a provider fault.
			return llm.Provider{}, httpx.Err(http.StatusConflict, "credential_unreadable",
				"The stored credential cannot be decrypted. Re-enter the API key for this provider.")
		}
		p.APIKey = plain
	}
	return p, nil
}

func (s *Store) sealKey(key string) ([]byte, string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, "", nil
	}
	cipher, err := s.box.Seal(key)
	if err != nil {
		return nil, "", fmt.Errorf("seal api key: %w", err)
	}
	return cipher, secrets.Hint(key), nil
}

/* ----------------------------------------------------------------- bindings */

func (s *Store) ListBindings(ctx context.Context) ([]Binding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.tier, b.provider_id, p.key, b.model, b.temperature, b.top_p,
		       b.max_tokens, b.timeout_ms, b.updated_at
		FROM model_bindings b
		JOIN model_providers p ON p.id = b.provider_id
		ORDER BY b.tier`)
	if err != nil {
		return nil, fmt.Errorf("list bindings: %w", err)
	}
	defer rows.Close()

	out := []Binding{}
	for rows.Next() {
		var b Binding
		if err := rows.Scan(&b.Tier, &b.ProviderID, &b.ProviderKey, &b.Model,
			&b.Temperature, &b.TopP, &b.MaxTokens, &b.TimeoutMS, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan binding: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

type BindingInput struct {
	Tier        string    `json:"tier"`
	ProviderID  uuid.UUID `json:"provider_id"`
	Model       string    `json:"model"`
	Temperature float64   `json:"temperature"`
	TopP        float64   `json:"top_p"`
	MaxTokens   int       `json:"max_tokens"`
	TimeoutMS   int       `json:"timeout_ms"`
}

// Validate mirrors the CHECK constraints. The database is the second line;
// rejecting here is what gives an operator a usable message instead of a
// constraint name.
func (in BindingInput) Validate() []string {
	if !validTier(in.Tier) {
		return []string{fmt.Sprintf("%q is not a tier; expected one of %s",
			in.Tier, strings.Join(Tiers, ", "))}
	}

	var problems []string
	where := "tier " + in.Tier

	if in.ProviderID == uuid.Nil {
		problems = append(problems, where+": provider_id is required")
	}
	if strings.TrimSpace(in.Model) == "" {
		problems = append(problems, where+": model is required")
	}
	if in.Temperature < 0 || in.Temperature > 2 {
		problems = append(problems, where+": temperature must be between 0 and 2")
	}
	if in.TopP <= 0 || in.TopP > 1 {
		problems = append(problems, where+": top_p must be greater than 0 and at most 1")
	}
	if in.MaxTokens < 1 || in.MaxTokens > 131072 {
		problems = append(problems, where+": max_tokens must be between 1 and 131072")
	}
	if in.TimeoutMS < 200 || in.TimeoutMS > 600000 {
		problems = append(problems, where+": timeout_ms must be between 200 and 600000")
	}
	return problems
}

// PutBindings replaces the whole set in one transaction. A half-applied
// routing table is worse than a rejected one.
func (s *Store) PutBindings(ctx context.Context, actorID uuid.UUID, in []BindingInput) ([]Binding, error) {
	var problems []string
	seen := map[string]struct{}{}
	for _, b := range in {
		problems = append(problems, b.Validate()...)
		if _, dup := seen[b.Tier]; dup {
			problems = append(problems, fmt.Sprintf("tier %s appears twice", b.Tier))
		}
		seen[b.Tier] = struct{}{}
	}
	if len(problems) > 0 {
		return nil, invalid("invalid_binding", "The model routing is not valid.", problems)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin put bindings: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// A PUT is the whole table. A tier left out of the payload is a tier the
	// operator un-routed, and leaving a stale row behind would mean the page
	// shows one thing and the router does another.
	keep := make([]string, 0, len(in))
	for _, b := range in {
		keep = append(keep, b.Tier)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM model_bindings WHERE tier <> ALL($1)`, keep); err != nil {
		return nil, fmt.Errorf("remove unrouted tiers: %w", err)
	}

	for _, b := range in {
		if _, err := tx.Exec(ctx, `
			INSERT INTO model_bindings
				(tier, provider_id, model, temperature, top_p, max_tokens, timeout_ms, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (tier) DO UPDATE SET
				provider_id = EXCLUDED.provider_id,
				model       = EXCLUDED.model,
				temperature = EXCLUDED.temperature,
				top_p       = EXCLUDED.top_p,
				max_tokens  = EXCLUDED.max_tokens,
				timeout_ms  = EXCLUDED.timeout_ms,
				updated_by  = EXCLUDED.updated_by`,
			b.Tier, b.ProviderID, strings.TrimSpace(b.Model), b.Temperature,
			b.TopP, b.MaxTokens, b.TimeoutMS, actorID); err != nil {
			if isForeignKeyViolation(err) {
				return nil, httpx.ErrBadRequest("That provider does not exist.")
			}
			return nil, fmt.Errorf("upsert binding %s: %w", b.Tier, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit put bindings: %w", err)
	}
	return s.ListBindings(ctx)
}

// Resolve returns everything needed to make a call for a tier. This is the
// function the grader and the judge will use; the connection test uses it now
// so the routing table is exercised rather than merely stored.
func (s *Store) Resolve(ctx context.Context, tier string) (llm.Provider, Binding, error) {
	if !validTier(tier) {
		return llm.Provider{}, Binding{}, httpx.ErrBadRequest("Unknown model tier.")
	}

	var b Binding
	err := s.pool.QueryRow(ctx, `
		SELECT b.tier, b.provider_id, p.key, b.model, b.temperature, b.top_p,
		       b.max_tokens, b.timeout_ms, b.updated_at
		FROM model_bindings b
		JOIN model_providers p ON p.id = b.provider_id
		WHERE b.tier = $1`, tier).
		Scan(&b.Tier, &b.ProviderID, &b.ProviderKey, &b.Model, &b.Temperature,
			&b.TopP, &b.MaxTokens, &b.TimeoutMS, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return llm.Provider{}, Binding{}, httpx.ErrBadRequest(
			"No model is bound to the " + tier + " tier yet.")
	}
	if err != nil {
		return llm.Provider{}, Binding{}, fmt.Errorf("resolve tier %s: %w", tier, err)
	}

	provider, err := s.credentials(ctx, b.ProviderID)
	if err != nil {
		return llm.Provider{}, Binding{}, err
	}
	return provider, b, nil
}

/* -------------------------------------------------------------------- utils */

func validTier(tier string) bool {
	for _, t := range Tiers {
		if t == tier {
			return true
		}
	}
	return false
}

func baseURLProblems(raw string) []string {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return []string{"base_url is required"}
	case !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://"):
		return []string{"base_url must start with http:// or https://"}
	}
	return nil
}

func keyPattern(s string) bool {
	if len(s) < 2 || len(s) > 48 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		case r == '_' && i > 0:
		default:
			return false
		}
	}
	return true
}

func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	return &t
}

func invalid(code, message string, problems []string) error {
	return httpx.Err(http.StatusUnprocessableEntity, code, message).
		WithFields(map[string]any{"problems": problems})
}

func isUniqueViolation(err error) bool { return sqlState(err) == "23505" }

func isForeignKeyViolation(err error) bool { return sqlState(err) == "23503" }

func sqlState(err error) string {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState()
	}
	return ""
}
