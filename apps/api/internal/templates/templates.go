// Package templates owns assessment templates and their immutable versions.
//
// A version becomes immutable once an assessment references it, so a template
// can keep evolving while assessments are in flight. Nothing here knows what a
// moot court is: a template is a stage list, a participation shape and a
// rubric.
package templates

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
	"github.com/slmlabs/megamoot/apps/api/internal/rubrics"
	"github.com/slmlabs/megamoot/apps/api/internal/spec"
)

type Template struct {
	ID             uuid.UUID `json:"id"`
	AssessmentType string    `json:"assessment_type"`
	Key            string    `json:"key"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Versions       []Version `json:"versions,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type Version struct {
	ID            uuid.UUID          `json:"id"`
	TemplateID    uuid.UUID          `json:"template_id"`
	Version       int                `json:"version"`
	RubricID      uuid.UUID          `json:"rubric_id"`
	Status        string             `json:"status"`
	Stages        []spec.Stage       `json:"stages"`
	Participation spec.Participation `json:"participation"`
	Defaults      json.RawMessage    `json:"defaults,omitempty"`
	PublishedAt   *time.Time         `json:"published_at"`
	CreatedAt     time.Time          `json:"created_at"`
}

const (
	StatusDraft      = "draft"
	StatusPublished  = "published"
	StatusDeprecated = "deprecated"
)

type Store struct {
	pool    *pgxpool.Pool
	rubrics *rubrics.Store
}

func NewStore(pool *pgxpool.Pool, rubricStore *rubrics.Store) *Store {
	return &Store{pool: pool, rubrics: rubricStore}
}

func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Template, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.assessment_type_key, t.key, t.name, t.description, t.created_at,
		       v.id, v.version, v.rubric_id, v.status, v.published_at, v.created_at
		FROM assessment_templates t
		LEFT JOIN assessment_template_versions v ON v.template_id = t.id
		WHERE t.organization_id = $1 AND t.archived_at IS NULL
		ORDER BY t.name, v.version DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	defer rows.Close()

	var out []Template
	index := map[uuid.UUID]int{}

	for rows.Next() {
		var t Template
		var vID, rubricID *uuid.UUID
		var version *int
		var status *string
		var publishedAt, versionCreated *time.Time

		if err := rows.Scan(&t.ID, &t.AssessmentType, &t.Key, &t.Name, &t.Description,
			&t.CreatedAt, &vID, &version, &rubricID, &status,
			&publishedAt, &versionCreated); err != nil {
			return nil, fmt.Errorf("scan template: %w", err)
		}

		pos, ok := index[t.ID]
		if !ok {
			t.Versions = []Version{}
			out = append(out, t)
			pos = len(out) - 1
			index[t.ID] = pos
		}
		if vID == nil {
			continue
		}
		out[pos].Versions = append(out[pos].Versions, Version{
			ID: *vID, TemplateID: t.ID, Version: *version, RubricID: *rubricID,
			Status: *status, PublishedAt: publishedAt, CreatedAt: *versionCreated,
		})
	}
	return out, rows.Err()
}

// Version loads one version with its full stage list. Used by assignment
// creation, which materialises the stages.
func (s *Store) Version(ctx context.Context, q querier, orgID, versionID uuid.UUID) (Version, error) {
	var v Version
	var stagesRaw, participationRaw []byte

	err := q.QueryRow(ctx, `
		SELECT v.id, v.template_id, v.version, v.rubric_id, v.status, v.stages,
		       v.participation, v.defaults, v.published_at, v.created_at
		FROM assessment_template_versions v
		JOIN assessment_templates t ON t.id = v.template_id
		WHERE v.id = $1 AND t.organization_id = $2`, versionID, orgID).
		Scan(&v.ID, &v.TemplateID, &v.Version, &v.RubricID, &v.Status, &stagesRaw,
			&participationRaw, &v.Defaults, &v.PublishedAt, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, httpx.ErrNotFound()
	}
	if err != nil {
		return Version{}, fmt.Errorf("load template version: %w", err)
	}

	if v.Stages, err = spec.DecodeStages(stagesRaw); err != nil {
		return Version{}, err
	}
	if v.Participation, err = spec.DecodeParticipation(participationRaw); err != nil {
		return Version{}, err
	}
	return v, nil
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type CreateTemplateInput struct {
	AssessmentType string `json:"assessment_type"`
	Key            string `json:"key"`
	Name           string `json:"name"`
	Description    string `json:"description"`
}

func (s *Store) CreateTemplate(ctx context.Context, orgID, actorID uuid.UUID,
	in CreateTemplateInput) (Template, error) {

	if strings.TrimSpace(in.Key) == "" || strings.TrimSpace(in.Name) == "" {
		return Template{}, httpx.ErrBadRequest("Key and name are required.")
	}

	out := Template{AssessmentType: in.AssessmentType, Key: in.Key,
		Name: in.Name, Description: in.Description, Versions: []Version{}}

	err := s.pool.QueryRow(ctx, `
		INSERT INTO assessment_templates
			(organization_id, assessment_type_key, key, name, description, created_by)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created_at`,
		orgID, in.AssessmentType, in.Key, in.Name, in.Description, actorID).
		Scan(&out.ID, &out.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return Template{}, httpx.ErrConflict("A template with that key already exists.")
		}
		if isForeignKeyViolation(err) {
			return Template{}, httpx.ErrBadRequest("Unknown assessment type.")
		}
		return Template{}, fmt.Errorf("create template: %w", err)
	}
	return out, nil
}

type CreateVersionInput struct {
	RubricID      uuid.UUID          `json:"rubric_id"`
	Stages        []spec.Stage       `json:"stages"`
	Participation spec.Participation `json:"participation"`
	Defaults      json.RawMessage    `json:"defaults,omitempty"`
}

// CreateVersion validates the stage list against the rubric before storing it.
// An invalid template caught here is a form error; caught later it is a
// student mid-assessment with a stage that cannot complete.
func (s *Store) CreateVersion(ctx context.Context, orgID, actorID, templateID uuid.UUID,
	in CreateVersionInput) (Version, error) {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Version{}, fmt.Errorf("begin create version: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM assessment_templates
		               WHERE id = $1 AND organization_id = $2 AND archived_at IS NULL)`,
		templateID, orgID).Scan(&exists); err != nil {
		return Version{}, fmt.Errorf("verify template: %w", err)
	}
	if !exists {
		return Version{}, httpx.ErrNotFound()
	}

	criterionKeys, err := s.rubrics.CriterionKeys(ctx, tx, orgID, in.RubricID)
	if err != nil {
		return Version{}, err
	}

	if err := spec.ValidateStages(in.Stages, criterionKeys); err != nil {
		return Version{}, asValidationFailure(err)
	}
	if err := in.Participation.Validate(); err != nil {
		return Version{}, asValidationFailure(err)
	}

	stagesRaw, err := json.Marshal(in.Stages)
	if err != nil {
		return Version{}, fmt.Errorf("encode stages: %w", err)
	}
	participationRaw, err := json.Marshal(in.Participation)
	if err != nil {
		return Version{}, fmt.Errorf("encode participation: %w", err)
	}
	defaults := in.Defaults
	if len(defaults) == 0 {
		defaults = json.RawMessage(`{}`)
	}

	out := Version{TemplateID: templateID, RubricID: in.RubricID, Status: StatusDraft,
		Stages: in.Stages, Participation: in.Participation, Defaults: defaults}

	err = tx.QueryRow(ctx, `
		INSERT INTO assessment_template_versions
			(template_id, version, rubric_id, stages, participation, defaults, created_by)
		VALUES (
			$1,
			(SELECT coalesce(max(version), 0) + 1
			 FROM assessment_template_versions WHERE template_id = $1),
			$2, $3, $4, $5, $6)
		RETURNING id, version, created_at`,
		templateID, in.RubricID, stagesRaw, participationRaw, defaults, actorID).
		Scan(&out.ID, &out.Version, &out.CreatedAt)
	if err != nil {
		return Version{}, fmt.Errorf("create template version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Version{}, fmt.Errorf("commit create version: %w", err)
	}
	return out, nil
}

// Publish makes a version usable by assessments. Draft versions cannot be
// instantiated, so an unfinished template cannot reach a student.
func (s *Store) Publish(ctx context.Context, orgID, versionID uuid.UUID) (Version, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE assessment_template_versions v
		SET status = 'published', published_at = now()
		FROM assessment_templates t
		WHERE v.id = $1 AND t.id = v.template_id AND t.organization_id = $2
		  AND v.status = 'draft'`, versionID, orgID)
	if err != nil {
		return Version{}, fmt.Errorf("publish template version: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return Version{}, httpx.ErrConflict("That version does not exist or is not a draft.")
	}
	return s.Version(ctx, s.pool, orgID, versionID)
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
	httpx.JSON(w, r, http.StatusOK, map[string]any{"templates": out})
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	var in CreateTemplateInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	out, err := h.store.CreateTemplate(r.Context(), p.OrganizationID, p.UserID, in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.audit.Record(r.Context(), audit.Entry{
		OrganizationID: &p.OrganizationID, ActorUserID: &p.UserID,
		Action: "template.create", TargetKind: "template", TargetID: &out.ID,
		After:     map[string]any{"key": out.Key},
		RequestID: httpx.RequestIDFrom(r.Context()),
	})
	httpx.JSON(w, r, http.StatusCreated, out)
}

func (h *Handlers) CreateVersion(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	templateID, err := uuid.Parse(chi.URLParam(r, "templateID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid template id."))
		return
	}

	var in CreateVersionInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out, err := h.store.CreateVersion(r.Context(), p.OrganizationID, p.UserID, templateID, in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.audit.Record(r.Context(), audit.Entry{
		OrganizationID: &p.OrganizationID, ActorUserID: &p.UserID,
		Action: "template.version.create", TargetKind: "template_version", TargetID: &out.ID,
		After:     map[string]any{"version": out.Version, "stages": len(out.Stages)},
		RequestID: httpx.RequestIDFrom(r.Context()),
	})
	httpx.JSON(w, r, http.StatusCreated, out)
}

func (h *Handlers) GetVersion(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	versionID, err := uuid.Parse(chi.URLParam(r, "versionID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid version id."))
		return
	}
	out, err := h.store.Version(r.Context(), h.store.pool, p.OrganizationID, versionID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (h *Handlers) PublishVersion(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	versionID, err := uuid.Parse(chi.URLParam(r, "versionID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid version id."))
		return
	}
	out, err := h.store.Publish(r.Context(), p.OrganizationID, versionID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.audit.Record(r.Context(), audit.Entry{
		OrganizationID: &p.OrganizationID, ActorUserID: &p.UserID,
		Action: "template.version.publish", TargetKind: "template_version", TargetID: &out.ID,
		After:     map[string]any{"version": out.Version},
		RequestID: httpx.RequestIDFrom(r.Context()),
	})
	httpx.JSON(w, r, http.StatusOK, out)
}

// asValidationFailure turns a spec validation error into a 422 carrying every
// problem, so a template author fixes them in one pass.
func asValidationFailure(err error) error {
	var v *spec.ValidationError
	if errors.As(err, &v) {
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_template",
			"The template configuration is not valid.").
			WithFields(map[string]any{"problems": v.Problems})
	}
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23503"
}
