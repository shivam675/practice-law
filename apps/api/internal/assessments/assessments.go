// Package assessments owns assessment instances and the assignments that
// attach teams to them.
//
// An assessment is a published template version plus a schedule and a cohort.
// An assignment is the unit of work, of grading and of reporting.
package assessments

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

	"github.com/intelimek/megamoot/apps/api/internal/audit"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/intelimek/megamoot/apps/api/internal/teams"
	"github.com/intelimek/megamoot/apps/api/internal/templates"
	"github.com/intelimek/megamoot/apps/api/internal/workflow"
)

const (
	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusRunning   = "running"
	StatusClosed    = "closed"
	StatusArchived  = "archived"
)

type Assessment struct {
	ID                uuid.UUID       `json:"id"`
	TemplateVersionID uuid.UUID       `json:"template_version_id"`
	TemplateName      string          `json:"template_name"`
	AssessmentType    string          `json:"assessment_type"`
	Title             string          `json:"title"`
	Description       string          `json:"description"`
	Status            string          `json:"status"`
	Config            json.RawMessage `json:"config"`
	OpensAt           *time.Time      `json:"opens_at"`
	ClosesAt          *time.Time      `json:"closes_at"`
	PublishedAt       *time.Time      `json:"published_at"`
	AssignmentCount   int             `json:"assignment_count"`
	CreatedAt         time.Time       `json:"created_at"`
}

type Store struct {
	pool      *pgxpool.Pool
	templates *templates.Store
	teams     *teams.Store
	engine    *workflow.Engine
}

func NewStore(pool *pgxpool.Pool, templateStore *templates.Store,
	teamStore *teams.Store, engine *workflow.Engine) *Store {
	return &Store{pool: pool, templates: templateStore, teams: teamStore, engine: engine}
}

func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Assessment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.template_version_id, t.name, t.assessment_type_key,
		       a.title, a.description, a.status, a.config,
		       a.opens_at, a.closes_at, a.published_at, a.created_at,
		       (SELECT count(*)::int FROM assignments WHERE assessment_id = a.id)
		FROM assessments a
		JOIN assessment_template_versions v ON v.id = a.template_version_id
		JOIN assessment_templates t ON t.id = v.template_id
		WHERE a.organization_id = $1 AND a.status <> 'archived'
		ORDER BY coalesce(a.opens_at, a.created_at) DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list assessments: %w", err)
	}
	defer rows.Close()

	out := []Assessment{}
	for rows.Next() {
		var a Assessment
		if err := rows.Scan(&a.ID, &a.TemplateVersionID, &a.TemplateName, &a.AssessmentType,
			&a.Title, &a.Description, &a.Status, &a.Config, &a.OpensAt, &a.ClosesAt,
			&a.PublishedAt, &a.CreatedAt, &a.AssignmentCount); err != nil {
			return nil, fmt.Errorf("scan assessment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, q querier, orgID, id uuid.UUID) (Assessment, error) {
	var a Assessment
	err := q.QueryRow(ctx, `
		SELECT a.id, a.template_version_id, t.name, t.assessment_type_key,
		       a.title, a.description, a.status, a.config,
		       a.opens_at, a.closes_at, a.published_at, a.created_at, 0
		FROM assessments a
		JOIN assessment_template_versions v ON v.id = a.template_version_id
		JOIN assessment_templates t ON t.id = v.template_id
		WHERE a.id = $1 AND a.organization_id = $2`, id, orgID).
		Scan(&a.ID, &a.TemplateVersionID, &a.TemplateName, &a.AssessmentType,
			&a.Title, &a.Description, &a.Status, &a.Config, &a.OpensAt, &a.ClosesAt,
			&a.PublishedAt, &a.CreatedAt, &a.AssignmentCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Assessment{}, httpx.ErrNotFound()
	}
	if err != nil {
		return Assessment{}, fmt.Errorf("load assessment: %w", err)
	}
	return a, nil
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type CreateInput struct {
	TemplateVersionID uuid.UUID       `json:"template_version_id"`
	Title             string          `json:"title"`
	Description       string          `json:"description"`
	OpensAt           *time.Time      `json:"opens_at"`
	ClosesAt          *time.Time      `json:"closes_at"`
	Config            json.RawMessage `json:"config,omitempty"`
}

func (s *Store) Create(ctx context.Context, orgID, actorID uuid.UUID, in CreateInput) (Assessment, error) {
	if strings.TrimSpace(in.Title) == "" {
		return Assessment{}, httpx.ErrBadRequest("Title is required.")
	}
	if in.OpensAt == nil {
		return Assessment{}, httpx.ErrBadRequest("opens_at is required; stage deadlines are relative to it.")
	}
	if in.ClosesAt != nil && !in.ClosesAt.After(*in.OpensAt) {
		return Assessment{}, httpx.ErrBadRequest("closes_at must be after opens_at.")
	}

	// Only a published template version may be instantiated, so an unfinished
	// template cannot reach a student.
	version, err := s.templates.Version(ctx, s.pool, orgID, in.TemplateVersionID)
	if err != nil {
		return Assessment{}, err
	}
	if version.Status != templates.StatusPublished {
		return Assessment{}, httpx.ErrConflict("That template version is not published.")
	}

	config := in.Config
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}

	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO assessments
			(organization_id, template_version_id, title, description, config,
			 opens_at, closes_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		orgID, in.TemplateVersionID, in.Title, in.Description, config,
		in.OpensAt, in.ClosesAt, actorID).Scan(&id)
	if err != nil {
		return Assessment{}, fmt.Errorf("create assessment: %w", err)
	}

	return s.Get(ctx, s.pool, orgID, id)
}

// Publish opens an assessment for assignment. Draft assessments accept no
// teams, so a half-configured assessment cannot be assigned by accident.
func (s *Store) Publish(ctx context.Context, orgID, id uuid.UUID) (Assessment, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE assessments SET status = 'published', published_at = now()
		WHERE id = $1 AND organization_id = $2 AND status = 'draft'
		  AND opens_at IS NOT NULL`, id, orgID)
	if err != nil {
		return Assessment{}, fmt.Errorf("publish assessment: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return Assessment{}, httpx.ErrConflict(
			"That assessment does not exist, is not a draft, or has no opening time.")
	}
	return s.Get(ctx, s.pool, orgID, id)
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
	httpx.JSON(w, r, http.StatusOK, map[string]any{"assessments": out})
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "assessmentID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid assessment id."))
		return
	}
	out, err := h.store.Get(r.Context(), h.store.pool, p.OrganizationID, id)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, out)
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
		OrganizationID: &p.OrganizationID, ActorUserID: &p.UserID,
		Action: "assessment.create", TargetKind: "assessment", TargetID: &out.ID,
		After:     map[string]any{"title": out.Title},
		RequestID: httpx.RequestIDFrom(r.Context()),
	})
	httpx.JSON(w, r, http.StatusCreated, out)
}

func (h *Handlers) Publish(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "assessmentID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid assessment id."))
		return
	}
	out, err := h.store.Publish(r.Context(), p.OrganizationID, id)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.audit.Record(r.Context(), audit.Entry{
		OrganizationID: &p.OrganizationID, ActorUserID: &p.UserID,
		Action: "assessment.publish", TargetKind: "assessment", TargetID: &out.ID,
		After:     map[string]any{"opens_at": out.OpensAt},
		RequestID: httpx.RequestIDFrom(r.Context()),
	})
	httpx.JSON(w, r, http.StatusOK, out)
}
