// Package rubrics owns scoring definitions.
//
// A rubric is a weighted list of criteria. Weights are relative and need not
// sum to anything in particular; totals are normalised in application code.
// No model ever produces a total. See ADR 0005.
package rubrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/slmlabs/megamoot/apps/api/internal/audit"
	"github.com/slmlabs/megamoot/apps/api/internal/auth"
	"github.com/slmlabs/megamoot/apps/api/internal/httpx"
)

type Criterion struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Weight      float64   `json:"weight"`
	MaxScore    float64   `json:"max_score"`
	Scope       []string  `json:"scope"`
	Guidance    string    `json:"guidance"`
	SortOrder   int       `json:"sort_order"`
}

type Rubric struct {
	ID          uuid.UUID   `json:"id"`
	Key         string      `json:"key"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Criteria    []Criterion `json:"criteria"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// CriterionKeys returns the criterion keys of a rubric, which is what stage
// validation checks a template's evaluation stages against.
func (s *Store) CriterionKeys(ctx context.Context, tx pgx.Tx, orgID, rubricID uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT c.key
		FROM rubric_criteria c
		JOIN rubrics r ON r.id = c.rubric_id
		WHERE c.rubric_id = $1 AND r.organization_id = $2
		ORDER BY c.sort_order, c.key`, rubricID, orgID)
	if err != nil {
		return nil, fmt.Errorf("load criterion keys: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan criterion key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, httpx.ErrBadRequest("That rubric does not exist or has no criteria.")
	}
	return keys, nil
}

func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Rubric, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.key, r.name, r.description,
		       c.id, c.key, c.name, c.description, c.weight, c.max_score,
		       c.scope, c.guidance, c.sort_order
		FROM rubrics r
		LEFT JOIN rubric_criteria c ON c.rubric_id = r.id
		WHERE r.organization_id = $1
		ORDER BY r.name, c.sort_order, c.key`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list rubrics: %w", err)
	}
	defer rows.Close()

	var out []Rubric
	index := map[uuid.UUID]int{}

	for rows.Next() {
		var r Rubric
		var c Criterion
		var cID *uuid.UUID
		var cKey, cName, cDesc, cGuidance *string
		var weight, maxScore *float64
		var scope []string
		var sortOrder *int

		if err := rows.Scan(&r.ID, &r.Key, &r.Name, &r.Description,
			&cID, &cKey, &cName, &cDesc, &weight, &maxScore,
			&scope, &cGuidance, &sortOrder); err != nil {
			return nil, fmt.Errorf("scan rubric: %w", err)
		}

		pos, ok := index[r.ID]
		if !ok {
			r.Criteria = []Criterion{}
			out = append(out, r)
			pos = len(out) - 1
			index[r.ID] = pos
		}
		if cID == nil {
			continue
		}
		c = Criterion{
			ID: *cID, Key: *cKey, Name: *cName, Description: *cDesc,
			Weight: *weight, MaxScore: *maxScore, Scope: scope,
			Guidance: *cGuidance, SortOrder: *sortOrder,
		}
		out[pos].Criteria = append(out[pos].Criteria, c)
	}
	return out, rows.Err()
}

type CreateInput struct {
	Key         string           `json:"key"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Criteria    []CriterionInput `json:"criteria"`
}

type CriterionInput struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Weight      float64  `json:"weight"`
	MaxScore    float64  `json:"max_score"`
	Scope       []string `json:"scope"`
	Guidance    string   `json:"guidance"`
}

func (in CreateInput) validate() error {
	var problems []string
	if !keyPattern(in.Key) {
		problems = append(problems, "key must be lowercase letters, digits and underscores")
	}
	if strings.TrimSpace(in.Name) == "" {
		problems = append(problems, "name is required")
	}
	if len(in.Criteria) == 0 {
		problems = append(problems, "a rubric needs at least one criterion")
	}

	seen := map[string]struct{}{}
	for i, c := range in.Criteria {
		where := fmt.Sprintf("criteria[%d]", i)
		if !keyPattern(c.Key) {
			problems = append(problems, where+": key must be lowercase letters, digits and underscores")
		}
		if _, dup := seen[c.Key]; dup {
			problems = append(problems, fmt.Sprintf("%s: duplicate key %q", where, c.Key))
		}
		seen[c.Key] = struct{}{}

		if strings.TrimSpace(c.Name) == "" {
			problems = append(problems, where+": name is required")
		}
		if c.Weight <= 0 {
			problems = append(problems, where+": weight must be greater than zero")
		}
		if c.MaxScore <= 0 {
			problems = append(problems, where+": max_score must be greater than zero")
		}
	}

	if len(problems) > 0 {
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_rubric",
			"The rubric is not valid.").WithFields(map[string]any{"problems": problems})
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

func (s *Store) Create(ctx context.Context, orgID, actorID uuid.UUID, in CreateInput) (Rubric, error) {
	if err := in.validate(); err != nil {
		return Rubric{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Rubric{}, fmt.Errorf("begin create rubric: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	out := Rubric{Key: in.Key, Name: in.Name, Description: in.Description}
	err = tx.QueryRow(ctx, `
		INSERT INTO rubrics (organization_id, key, name, description, created_by)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		orgID, in.Key, in.Name, in.Description, actorID).Scan(&out.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return Rubric{}, httpx.ErrConflict("A rubric with that key already exists.")
		}
		return Rubric{}, fmt.Errorf("create rubric: %w", err)
	}

	for i, c := range in.Criteria {
		var id uuid.UUID
		scope := c.Scope
		if scope == nil {
			scope = []string{}
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO rubric_criteria
				(rubric_id, key, name, description, weight, max_score, scope,
				 guidance, sort_order)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
			out.ID, c.Key, c.Name, c.Description, c.Weight, c.MaxScore,
			scope, c.Guidance, i).Scan(&id); err != nil {
			return Rubric{}, fmt.Errorf("create criterion %q: %w", c.Key, err)
		}
		out.Criteria = append(out.Criteria, Criterion{
			ID: id, Key: c.Key, Name: c.Name, Description: c.Description,
			Weight: c.Weight, MaxScore: c.MaxScore, Scope: scope,
			Guidance: c.Guidance, SortOrder: i,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return Rubric{}, fmt.Errorf("commit create rubric: %w", err)
	}
	return out, nil
}

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
	httpx.JSON(w, r, http.StatusOK, map[string]any{"rubrics": out})
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	var in CreateInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out, err := h.store.Create(r.Context(), p.OrganizationID, p.UserID, in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.audit.Record(r.Context(), audit.Entry{
		OrganizationID: &p.OrganizationID,
		ActorUserID:    &p.UserID,
		Action:         "rubric.create",
		TargetKind:     "rubric",
		TargetID:       &out.ID,
		After:          map[string]any{"key": out.Key, "criteria": len(out.Criteria)},
		RequestID:      httpx.RequestIDFrom(r.Context()),
	})

	httpx.JSON(w, r, http.StatusCreated, out)
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "rubricID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid rubric id."))
		return
	}

	all, err := h.store.List(r.Context(), p.OrganizationID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	for _, rub := range all {
		if rub.ID == id {
			httpx.JSON(w, r, http.StatusOK, rub)
			return
		}
	}
	httpx.Fail(w, r, httpx.ErrNotFound())
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}
