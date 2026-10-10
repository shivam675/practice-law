// Package aiprofiles owns the configuration of AI actors: prompt, parameters,
// personality and capabilities.
//
// A profile is organisation scoped, because how firm a judge is belongs to the
// institution running the assessment. Which model answers does not; that is
// platformcfg.
//
// Profiles are versioned rather than edited. A grade stamped with version 3 of
// a judge must stay reproducible after someone tunes it to version 4, so an
// edit inserts a new row and retires the old one.
package aiprofiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/slmlabs/megamoot/apps/api/internal/audit"
	"github.com/slmlabs/megamoot/apps/api/internal/auth"
	"github.com/slmlabs/megamoot/apps/api/internal/httpx"
)

// The closed sets. Each mirrors a CHECK constraint or, for capabilities, what
// the coordinator will actually enforce. A capability the coordinator does not
// know about would read as a granted power and be silently ignored, which is
// the worst of both.
var (
	Roles        = []string{"judge", "examiner", "interviewer", "opponent", "moderator", "evaluator"}
	Tiers        = []string{"monitor", "judge", "grader"}
	Capabilities = []string{"ask_question", "interrupt", "evaluate", "summarise"}
)

type Profile struct {
	ID                 uuid.UUID      `json:"id"`
	Key                string         `json:"key"`
	Name               string         `json:"name"`
	Role               string         `json:"role"`
	Version            int            `json:"version"`
	ModelTier          string         `json:"model_tier"`
	SystemPrompt       string         `json:"system_prompt"`
	Temperature        float64        `json:"temperature"`
	Voice              string         `json:"voice"`
	Personality        map[string]any `json:"personality"`
	InterruptionPolicy map[string]any `json:"interruption_policy"`
	Focus              []string       `json:"focus"`
	Capabilities       []string       `json:"capabilities"`
	RAGSources         []string       `json:"rag_sources"`
	IsActive           bool           `json:"is_active"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const columns = `id, key, name, role, version, model_tier, system_prompt,
	temperature, voice, personality, interruption_policy, focus, capabilities,
	rag_sources, is_active, created_at, updated_at`

func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Profile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+columns+`
		FROM ai_profiles
		WHERE organization_id = $1
		ORDER BY key, version DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list ai profiles: %w", err)
	}
	defer rows.Close()

	out := []Profile{}
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type Input struct {
	Key                string         `json:"key"`
	Name               string         `json:"name"`
	Role               string         `json:"role"`
	ModelTier          string         `json:"model_tier"`
	SystemPrompt       string         `json:"system_prompt"`
	Temperature        float64        `json:"temperature"`
	Voice              string         `json:"voice"`
	Personality        map[string]any `json:"personality"`
	InterruptionPolicy map[string]any `json:"interruption_policy"`
	Focus              []string       `json:"focus"`
	Capabilities       []string       `json:"capabilities"`
	RAGSources         []string       `json:"rag_sources"`
}

func (in Input) validate() error {
	var problems []string

	if !keyPattern(in.Key) {
		problems = append(problems, "key must be lowercase letters, digits and underscores")
	}
	if strings.TrimSpace(in.Name) == "" {
		problems = append(problems, "name is required")
	}
	if !oneOf(in.Role, Roles) {
		problems = append(problems, "role must be one of "+strings.Join(Roles, ", "))
	}
	if !oneOf(in.ModelTier, Tiers) {
		problems = append(problems, "model_tier must be one of "+strings.Join(Tiers, ", "))
	}
	if in.Temperature < 0 || in.Temperature > 2 {
		problems = append(problems, "temperature must be between 0 and 2")
	}
	var policy struct {
		Cooldown *int     `json:"cooldown_s"`
		Maximum  *int     `json:"max_per_stage"`
		Priority *float64 `json:"min_priority"`
	}
	encoded, err := json.Marshal(in.InterruptionPolicy)
	if err != nil || json.Unmarshal(encoded, &policy) != nil ||
		(policy.Cooldown != nil && (*policy.Cooldown < 1 || *policy.Cooldown > 3600)) ||
		(policy.Maximum != nil && (*policy.Maximum < 0 || *policy.Maximum > 100)) ||
		(policy.Priority != nil && (*policy.Priority < 0 || *policy.Priority > 1)) {
		problems = append(problems, "interruption policy requires cooldown_s 1–3600, max_per_stage 0–100 and min_priority 0–1")
	}
	for _, c := range in.Capabilities {
		if !oneOf(c, Capabilities) {
			problems = append(problems, fmt.Sprintf(
				"capability %q is not enforced by the coordinator; expected one of %s",
				c, strings.Join(Capabilities, ", ")))
		}
	}
	// A prompt is untrusted-adjacent: it is written by staff, but it ends up
	// in a system role, so its size is bounded like anything else that does.
	if len(in.SystemPrompt) > 20000 {
		problems = append(problems, "system_prompt must be at most 20000 characters")
	}

	if len(problems) > 0 {
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_ai_profile",
			"The profile is not valid.").WithFields(map[string]any{"problems": problems})
	}
	return nil
}

func (s *Store) Create(ctx context.Context, orgID, actorID uuid.UUID, in Input) (Profile, error) {
	if err := in.validate(); err != nil {
		return Profile{}, err
	}

	rows, err := s.pool.Query(ctx, `
		INSERT INTO ai_profiles
			(organization_id, key, name, role, version, model_tier, system_prompt,
			 temperature, voice, personality, interruption_policy, focus,
			 capabilities, rag_sources, created_by)
		VALUES ($1,$2,$3,$4,1,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING `+columns,
		orgID, in.Key, strings.TrimSpace(in.Name), in.Role, in.ModelTier,
		in.SystemPrompt, in.Temperature, in.Voice,
		orEmptyMap(in.Personality), orEmptyMap(in.InterruptionPolicy),
		orEmpty(in.Focus), orEmpty(in.Capabilities), orEmpty(in.RAGSources), actorID)
	if err != nil {
		return Profile{}, fmt.Errorf("create ai profile: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); isUniqueViolation(err) {
			return Profile{}, httpx.ErrConflict("A profile with that key already exists.")
		} else if err != nil {
			return Profile{}, fmt.Errorf("create ai profile: %w", err)
		}
		return Profile{}, fmt.Errorf("create ai profile: no row returned")
	}
	return scan(rows)
}

// Revise supersedes a profile. The previous version stays in the table,
// inactive, so anything stamped with it can still be explained.
func (s *Store) Revise(ctx context.Context, orgID, actorID, id uuid.UUID, in Input) (Profile, error) {
	if err := in.validate(); err != nil {
		return Profile{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("begin revise ai profile: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var currentKey string
	err = tx.QueryRow(ctx, `
		SELECT key FROM ai_profiles
		WHERE id = $1 AND organization_id = $2
		FOR UPDATE`, id, orgID).Scan(&currentKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, httpx.ErrNotFound()
	}
	if err != nil {
		return Profile{}, fmt.Errorf("load ai profile: %w", err)
	}
	if in.Key != currentKey {
		return Profile{}, httpx.ErrBadRequest(
			"A profile key is permanent. Create a new profile instead of renaming this one.")
	}

	if _, err := tx.Exec(ctx, `
		UPDATE ai_profiles SET is_active = false
		WHERE organization_id = $1 AND key = $2`, orgID, currentKey); err != nil {
		return Profile{}, fmt.Errorf("retire previous versions: %w", err)
	}

	rows, err := tx.Query(ctx, `
		INSERT INTO ai_profiles
			(organization_id, key, name, role, version, model_tier, system_prompt,
			 temperature, voice, personality, interruption_policy, focus,
			 capabilities, rag_sources, created_by)
		SELECT $1,$2,$3,$4, COALESCE(MAX(version), 0) + 1, $5,$6,$7,$8,$9,$10,$11,$12,$13,$14
		FROM ai_profiles WHERE organization_id = $1 AND key = $2
		RETURNING `+columns,
		orgID, currentKey, strings.TrimSpace(in.Name), in.Role, in.ModelTier,
		in.SystemPrompt, in.Temperature, in.Voice,
		orEmptyMap(in.Personality), orEmptyMap(in.InterruptionPolicy),
		orEmpty(in.Focus), orEmpty(in.Capabilities), orEmpty(in.RAGSources), actorID)
	if err != nil {
		return Profile{}, fmt.Errorf("insert ai profile version: %w", err)
	}

	if !rows.Next() {
		rows.Close()
		return Profile{}, fmt.Errorf("insert ai profile version: no row returned")
	}
	out, err := scan(rows)
	rows.Close()
	if err != nil {
		return Profile{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Profile{}, fmt.Errorf("commit revise ai profile: %w", err)
	}
	return out, nil
}

// SetActive toggles one version without creating another. Disabling a judge
// before a session is not a change to how it behaves.
func (s *Store) SetActive(ctx context.Context, orgID, id uuid.UUID, active bool) (Profile, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE ai_profiles SET is_active = $3
		WHERE id = $1 AND organization_id = $2
		RETURNING `+columns, id, orgID, active)
	if err != nil {
		return Profile{}, fmt.Errorf("set ai profile active: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Profile{}, fmt.Errorf("set ai profile active: %w", err)
		}
		return Profile{}, httpx.ErrNotFound()
	}
	return scan(rows)
}

/* ----------------------------------------------------------------- handlers */

type Handlers struct {
	store *Store
	audit *audit.Logger
}

func NewHandlers(store *Store, auditLog *audit.Logger) *Handlers {
	return &Handlers{store: store, audit: auditLog}
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	out, err := h.store.List(r.Context(), p.OrganizationID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{
		"profiles":     out,
		"roles":        Roles,
		"tiers":        Tiers,
		"capabilities": Capabilities,
	})
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	var in Input
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out, err := h.store.Create(r.Context(), p.OrganizationID, p.UserID, in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.record(r, p, "ai_profile.create", out)
	httpx.JSON(w, r, http.StatusCreated, out)
}

func (h *Handlers) Revise(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "profileID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid profile id."))
		return
	}

	var in Input
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out, err := h.store.Revise(r.Context(), p.OrganizationID, p.UserID, id, in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.record(r, p, "ai_profile.revise", out)
	httpx.JSON(w, r, http.StatusCreated, out)
}

func (h *Handlers) SetActive(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "profileID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid profile id."))
		return
	}

	var in struct {
		IsActive bool `json:"is_active"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out, err := h.store.SetActive(r.Context(), p.OrganizationID, id, in.IsActive)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.record(r, p, "ai_profile.set_active", out)
	httpx.JSON(w, r, http.StatusOK, out)
}

func (h *Handlers) record(r *http.Request, p auth.Principal, action string, out Profile) {
	orgID := p.OrganizationID
	h.audit.Record(r.Context(), audit.Entry{
		OrganizationID: &orgID,
		ActorUserID:    &p.UserID,
		Action:         action,
		TargetKind:     "ai_profile",
		TargetID:       &out.ID,
		After: map[string]any{
			"key": out.Key, "version": out.Version, "role": out.Role,
			"model_tier": out.ModelTier, "temperature": out.Temperature,
			"is_active": out.IsActive,
		},
		RequestID: httpx.RequestIDFrom(r.Context()),
	})
}

/* -------------------------------------------------------------------- utils */

func scan(rows pgx.Rows) (Profile, error) {
	var p Profile
	if err := rows.Scan(&p.ID, &p.Key, &p.Name, &p.Role, &p.Version, &p.ModelTier,
		&p.SystemPrompt, &p.Temperature, &p.Voice, &p.Personality,
		&p.InterruptionPolicy, &p.Focus, &p.Capabilities, &p.RAGSources,
		&p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return Profile{}, fmt.Errorf("scan ai profile: %w", err)
	}
	return p, nil
}

func oneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if a == v {
			return true
		}
	}
	return false
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func orEmptyMap(v map[string]any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return v
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

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}
