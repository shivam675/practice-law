package assessments

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/intelimek/megamoot/apps/api/internal/spec"
	"github.com/intelimek/megamoot/apps/api/internal/workflow"
)

// Resource is a piece of assessment source material as a participant sees it.
type Resource struct {
	ID    uuid.UUID `json:"id"`
	Kind  string    `json:"kind"`
	Title string    `json:"title"`
	// Body is the extracted text. Present only once the stage that reveals
	// this resource kind is open.
	Body      string `json:"body,omitempty"`
	Available bool   `json:"available"`
}

// Resources returns the material visible to an assignment right now.
//
// Visibility is decided server-side from stage state, not by the client
// hiding things it was sent. A problem statement released early is an
// integrity failure, so the body is withheld until a stage reveals that kind.
func (s *Store) Resources(ctx context.Context, orgID, assignmentID uuid.UUID,
	forUser *uuid.UUID) ([]Resource, error) {

	var assessmentID, templateVersionID uuid.UUID
	var side string
	err := s.pool.QueryRow(ctx, `
		SELECT a.assessment_id, ass.template_version_id, a.side
		FROM assignments a
		JOIN assessments ass ON ass.id = a.assessment_id
		WHERE a.id = $1 AND a.organization_id = $2
		  AND ($3::uuid IS NULL OR EXISTS (
		        SELECT 1 FROM team_members m
		        WHERE m.team_id = a.team_id AND m.user_id = $3))`,
		assignmentID, orgID, forUser).Scan(&assessmentID, &templateVersionID, &side)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.ErrNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("load assignment for resources: %w", err)
	}

	revealed, err := s.revealedKinds(ctx, assignmentID, templateVersionID, orgID)
	if err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT ks.id, ks.kind, ks.title,
		       coalesce((
		         SELECT d.extracted_text FROM documents d
		         WHERE d.knowledge_source_id = ks.id AND d.parse_status = 'parsed'
		         ORDER BY d.created_at LIMIT 1
		       ), '')
		FROM knowledge_sources ks
		WHERE ks.organization_id = $1
		  AND (ks.assessment_id = $2 OR ks.assessment_id IS NULL)
		  AND ks.visibility IN ('all', $3)
		ORDER BY CASE ks.kind
		           WHEN 'problem' THEN 0 WHEN 'statute' THEN 1
		           WHEN 'authority' THEN 2 WHEN 'evidence' THEN 3 ELSE 4 END,
		         ks.title`,
		orgID, assessmentID, side)
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	defer rows.Close()

	out := []Resource{}
	for rows.Next() {
		var r Resource
		var body string
		if err := rows.Scan(&r.ID, &r.Kind, &r.Title, &body); err != nil {
			return nil, fmt.Errorf("scan resource: %w", err)
		}
		if _, ok := revealed[r.Kind]; ok {
			r.Available = true
			r.Body = body
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// revealedKinds collects the resource kinds that any started stage makes
// visible. Guidance is always available: a student needs the marking criteria
// before they write, not after.
func (s *Store) revealedKinds(ctx context.Context, assignmentID, templateVersionID,
	orgID uuid.UUID) (map[string]struct{}, error) {

	version, err := s.templates.Version(ctx, s.pool, orgID, templateVersionID)
	if err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT stage_id, status FROM assignment_stages WHERE assignment_id = $1`,
		assignmentID)
	if err != nil {
		return nil, fmt.Errorf("load stage states: %w", err)
	}
	defer rows.Close()

	status := map[string]string{}
	for rows.Next() {
		var id, st string
		if err := rows.Scan(&id, &st); err != nil {
			return nil, fmt.Errorf("scan stage state: %w", err)
		}
		status[id] = st
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	revealed := map[string]struct{}{"guidance": {}}
	for _, stage := range version.Stages {
		switch status[stage.ID] {
		case workflow.StagePending, workflow.StageSkipped, "":
			continue
		}
		if stage.Kind != spec.KindWait {
			continue
		}
		cfg, err := spec.DecodeConfig[spec.WaitConfig](stage)
		if err != nil {
			return nil, err
		}
		for _, kind := range cfg.VisibleResources {
			revealed[kind] = struct{}{}
		}
	}

	// Once a submission stage has opened, the material it is written against
	// is necessarily available.
	for _, stage := range version.Stages {
		if stage.Kind != spec.KindArtifactSubmission {
			continue
		}
		if st := status[stage.ID]; st == workflow.StageActive || st == workflow.StageGrace ||
			st == workflow.StageCompleted || st == workflow.StageExpired {
			for _, kind := range []string{"problem", "statute", "authority", "evidence"} {
				revealed[kind] = struct{}{}
			}
		}
	}

	return revealed, nil
}

func (h *Handlers) Resources(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	assignmentID, err := uuid.Parse(chi.URLParam(r, "assignmentID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid assignment id."))
		return
	}

	var forUser *uuid.UUID
	if !p.Can("knowledge.view") || !p.Can("assessment.view") {
		forUser = &p.UserID
	}

	out, err := h.store.Resources(r.Context(), p.OrganizationID, assignmentID, forUser)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"resources": out})
}
