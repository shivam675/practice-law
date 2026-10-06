// Package submissions handles uploaded artifacts: storage, extraction,
// structural compliance and locking.
//
// Locking is a conditional UPDATE rather than an application-level check,
// because a deadline on an assessment platform has to hold against two
// browser tabs and a retry.
package submissions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/intelimek/megamoot/apps/api/internal/aiclient"
	"github.com/intelimek/megamoot/apps/api/internal/audit"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/blob"
	"github.com/intelimek/megamoot/apps/api/internal/compliance"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/intelimek/megamoot/apps/api/internal/spec"
	"github.com/intelimek/megamoot/apps/api/internal/templates"
	"github.com/intelimek/megamoot/apps/api/internal/workflow"
)

type Artifact struct {
	ID          uuid.UUID          `json:"id"`
	StageID     string             `json:"stage_id"`
	Version     int                `json:"version"`
	Filename    string             `json:"filename"`
	ByteSize    int64              `json:"byte_size"`
	Pages       *int               `json:"pages"`
	WordCount   int                `json:"word_count"`
	IsLate      bool               `json:"is_late"`
	LockedAt    *time.Time         `json:"locked_at"`
	SubmittedAt *time.Time         `json:"submitted_at"`
	Compliance  *compliance.Report `json:"compliance,omitempty"`
	UploadedBy  *uuid.UUID         `json:"uploaded_by"`
}

type Store struct {
	pool      *pgxpool.Pool
	blobs     blob.Store
	ai        *aiclient.Client
	templates *templates.Store
	engine    *workflow.Engine
}

func NewStore(pool *pgxpool.Pool, blobs blob.Store, ai *aiclient.Client,
	templateStore *templates.Store, engine *workflow.Engine) *Store {
	return &Store{pool: pool, blobs: blobs, ai: ai, templates: templateStore, engine: engine}
}

// stageContext is everything needed to validate an upload, read in one query.
type stageContext struct {
	StageRowID        uuid.UUID
	AssignmentID      uuid.UUID
	TeamID            uuid.UUID
	Status            string
	Kind              string
	DueAt             *time.Time
	GraceUntil        *time.Time
	TemplateVersionID uuid.UUID
	AssignmentStatus  string
}

func (s *Store) loadStage(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, orgID, assignmentID uuid.UUID, stageID string) (stageContext, error) {

	var sc stageContext
	err := q.QueryRow(ctx, `
		SELECT st.id, st.assignment_id, a.team_id, st.status, st.stage_kind,
		       st.due_at, st.grace_until, ass.template_version_id, a.status
		FROM assignment_stages st
		JOIN assignments a ON a.id = st.assignment_id
		JOIN assessments ass ON ass.id = a.assessment_id
		WHERE st.assignment_id = $1 AND st.stage_id = $2 AND st.organization_id = $3`,
		assignmentID, stageID, orgID).
		Scan(&sc.StageRowID, &sc.AssignmentID, &sc.TeamID, &sc.Status, &sc.Kind,
			&sc.DueAt, &sc.GraceUntil, &sc.TemplateVersionID, &sc.AssignmentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return stageContext{}, httpx.ErrNotFound()
	}
	if err != nil {
		return stageContext{}, fmt.Errorf("load stage: %w", err)
	}
	return sc, nil
}

func (s *Store) isTeamMember(ctx context.Context, teamID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM team_members WHERE team_id = $1 AND user_id = $2)`,
		teamID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check team membership: %w", err)
	}
	return ok, nil
}

type UploadInput struct {
	OrganizationID uuid.UUID
	ActorID        uuid.UUID
	AssignmentID   uuid.UUID
	StageID        string
	Filename       string
	Data           []byte
}

// Upload validates, stores, extracts and checks a submission, then closes the
// stage if the template says a submission is final.
//
// The blob is written before the database row, so a failure can leave an
// unreferenced object. That is the safe direction: an orphaned blob costs disk,
// whereas a row pointing at a missing object is a broken submission a student
// cannot resubmit.
func (s *Store) Upload(ctx context.Context, in UploadInput) (Artifact, error) {
	sc, err := s.loadStage(ctx, s.pool, in.OrganizationID, in.AssignmentID, in.StageID)
	if err != nil {
		return Artifact{}, err
	}

	member, err := s.isTeamMember(ctx, sc.TeamID, in.ActorID)
	if err != nil {
		return Artifact{}, err
	}
	if !member {
		// Not 403: whether this assignment exists is not this caller's business.
		return Artifact{}, httpx.ErrNotFound()
	}

	if sc.Kind != string(spec.KindArtifactSubmission) {
		return Artifact{}, httpx.ErrConflict("That stage does not accept submissions.")
	}
	switch sc.Status {
	case workflow.StageActive, workflow.StageGrace:
	case workflow.StagePending:
		return Artifact{}, httpx.ErrConflict("That stage has not opened yet.")
	default:
		return Artifact{}, httpx.ErrConflict(
			fmt.Sprintf("That stage is closed (%s).", sc.Status))
	}

	version, err := s.templates.Version(ctx, s.pool, in.OrganizationID, sc.TemplateVersionID)
	if err != nil {
		return Artifact{}, err
	}
	stage, ok := spec.Find(version.Stages, in.StageID)
	if !ok {
		return Artifact{}, fmt.Errorf("stage %q is missing from template version %s",
			in.StageID, sc.TemplateVersionID)
	}
	cfg, err := spec.DecodeConfig[spec.ArtifactSubmissionConfig](stage)
	if err != nil {
		return Artifact{}, err
	}

	if err := s.checkNotLocked(ctx, in.AssignmentID, in.StageID); err != nil {
		return Artifact{}, err
	}

	if int64(len(in.Data)) > cfg.MaxBytes {
		return Artifact{}, httpx.Err(http.StatusRequestEntityTooLarge, "file_too_large",
			fmt.Sprintf("That file is %d bytes; the limit is %d.", len(in.Data), cfg.MaxBytes))
	}

	// The declared filename and content type come from the client. The bytes
	// decide, and the result must be in the template's allowed list.
	format, err := sniff(in.Data)
	if err != nil {
		return Artifact{}, httpx.Err(http.StatusUnsupportedMediaType, "unsupported_format",
			"That file is not a PDF, Word document or plain text file.")
	}
	if !allowed(cfg.Formats, format) {
		return Artifact{}, httpx.Err(http.StatusUnsupportedMediaType, "unsupported_format",
			fmt.Sprintf("This stage accepts %s; that file is a %s.",
				strings.Join(cfg.Formats, ", "), format))
	}

	extraction, err := s.ai.Extract(ctx, in.Filename, in.Data)
	if err != nil {
		var extractErr *aiclient.ExtractError
		if errors.As(err, &extractErr) && extractErr.Unsupported() {
			return Artifact{}, httpx.Err(http.StatusUnprocessableEntity, "unreadable_document",
				"That document could not be read. If it is a scanned PDF, submit a text PDF instead.")
		}
		return Artifact{}, fmt.Errorf("extract submission: %w", err)
	}

	report := s.runCompliance(cfg.FormatRules, extraction)

	digest := sha256.Sum256(in.Data)
	documentID := uuid.New()
	storageKey := path.Join("org", in.OrganizationID.String(), "assignment",
		in.AssignmentID.String(), in.StageID, documentID.String()+"."+format)

	if err := s.blobs.Put(ctx, storageKey, bytes.NewReader(in.Data),
		int64(len(in.Data)), contentTypeFor(format)); err != nil {
		return Artifact{}, fmt.Errorf("store submission: %w", err)
	}

	artifact, stageRowID, completeStage, err := s.record(ctx, in, sc, cfg, format,
		documentID, storageKey, digest[:], extraction, report)
	if err != nil {
		// Leave the blob: see the note on ordering above.
		return Artifact{}, err
	}

	if completeStage {
		err := s.engine.Apply(ctx, workflow.Request{
			Subject: workflow.SubjectStage, SubjectID: stageRowID,
			OrganizationID: in.OrganizationID, To: workflow.StageCompleted,
			Cause: "submission_received", Actor: workflow.UserActor(in.ActorID),
		})
		if err != nil && !errors.Is(err, workflow.ErrNoop) {
			return artifact, fmt.Errorf("close submission stage: %w", err)
		}
	}

	return artifact, nil
}

// checkNotLocked refuses a second submission once one is final.
func (s *Store) checkNotLocked(ctx context.Context, assignmentID uuid.UUID, stageID string) error {
	var lockedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT locked_at FROM artifacts
		WHERE assignment_id = $1 AND stage_id = $2
		ORDER BY version DESC LIMIT 1`, assignmentID, stageID).Scan(&lockedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check submission lock: %w", err)
	}
	if lockedAt != nil {
		return httpx.ErrConflict(
			"A submission has already been made for this stage and is locked.")
	}
	return nil
}

func (s *Store) runCompliance(ruleSetKey string, extraction aiclient.Extraction) *compliance.Report {
	if ruleSetKey == "" {
		return nil
	}
	ruleSet, err := compliance.Lookup(ruleSetKey)
	if err != nil {
		// A template naming a rule set that no longer exists is a
		// configuration bug. Recording a finding beats silently skipping the
		// structure check on a graded submission.
		return &compliance.Report{
			RuleSetKey: ruleSetKey,
			Fraction:   1,
			Findings: []compliance.Finding{{
				Rule:     "ruleset.unknown",
				Status:   compliance.StatusNotCheckable,
				Severity: compliance.SeverityWarning,
				Message: fmt.Sprintf("Structure could not be checked: this assessment "+
					"names an unknown rule set (%q). Tell your teacher.", ruleSetKey),
			}},
		}
	}

	pages := 0
	if extraction.Pages != nil {
		pages = *extraction.Pages
	}
	report := compliance.Check(ruleSet, compliance.Input{Text: extraction.Text, Pages: pages})
	return &report
}

func (s *Store) record(ctx context.Context, in UploadInput, sc stageContext,
	cfg spec.ArtifactSubmissionConfig, format string, documentID uuid.UUID,
	storageKey string, digest []byte, extraction aiclient.Extraction,
	report *compliance.Report) (Artifact, uuid.UUID, bool, error) {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Artifact{}, uuid.Nil, false, fmt.Errorf("begin record submission: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Re-read the stage under a lock: between validation and here the deadline
	// worker may have closed it.
	var status string
	var dueAt, graceUntil *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT status, due_at, grace_until FROM assignment_stages
		WHERE id = $1 AND organization_id = $2 FOR UPDATE`,
		sc.StageRowID, in.OrganizationID).Scan(&status, &dueAt, &graceUntil); err != nil {
		return Artifact{}, uuid.Nil, false, fmt.Errorf("lock stage: %w", err)
	}
	if status != workflow.StageActive && status != workflow.StageGrace {
		return Artifact{}, uuid.Nil, false, httpx.ErrConflict(
			fmt.Sprintf("That stage closed while the file was uploading (%s).", status))
	}

	var pages *int
	if extraction.Pages != nil {
		pages = extraction.Pages
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO documents
			(id, organization_id, storage_key, filename, content_type, byte_size,
			 sha256, parse_status, extracted_text, page_count)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'parsed',$8,$9)`,
		documentID, in.OrganizationID, storageKey, in.Filename, contentTypeFor(format),
		len(in.Data), digest, extraction.Text, pages); err != nil {
		return Artifact{}, uuid.Nil, false, fmt.Errorf("record document: %w", err)
	}

	// The server's clock decides lateness, not the client's.
	isLate := dueAt != nil && time.Now().After(*dueAt)

	complianceJSON := []byte(`{}`)
	if report != nil {
		raw, err := marshalReport(report)
		if err != nil {
			return Artifact{}, uuid.Nil, false, err
		}
		complianceJSON = raw
	}

	artifact := Artifact{StageID: in.StageID, Filename: in.Filename,
		ByteSize: int64(len(in.Data)), Pages: pages, IsLate: isLate,
		Compliance: report, UploadedBy: &in.ActorID}
	if report != nil {
		artifact.WordCount = report.WordCount
	}

	lockOnSubmit := cfg.LockOnSubmit

	err = tx.QueryRow(ctx, `
		INSERT INTO artifacts
			(organization_id, assignment_id, stage_id, version, document_id,
			 uploaded_by, compliance, submitted_at, locked_at, is_late)
		VALUES (
			$1, $2, $3,
			(SELECT coalesce(max(version), 0) + 1 FROM artifacts
			 WHERE assignment_id = $2 AND stage_id = $3),
			$4, $5, $6, now(),
			CASE WHEN $7 THEN now() ELSE NULL END,
			$8)
		RETURNING id, version, submitted_at, locked_at`,
		in.OrganizationID, in.AssignmentID, in.StageID, documentID, in.ActorID,
		complianceJSON, lockOnSubmit, isLate).
		Scan(&artifact.ID, &artifact.Version, &artifact.SubmittedAt, &artifact.LockedAt)
	if err != nil {
		return Artifact{}, uuid.Nil, false, fmt.Errorf("record artifact: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Artifact{}, uuid.Nil, false, fmt.Errorf("commit submission: %w", err)
	}

	return artifact, sc.StageRowID, lockOnSubmit, nil
}

func (s *Store) List(ctx context.Context, orgID, assignmentID uuid.UUID) ([]Artifact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.stage_id, a.version, d.filename, d.byte_size, d.page_count,
		       a.is_late, a.locked_at, a.submitted_at, a.uploaded_by, a.compliance
		FROM artifacts a
		JOIN documents d ON d.id = a.document_id
		WHERE a.assignment_id = $1 AND a.organization_id = $2
		ORDER BY a.stage_id, a.version DESC`, assignmentID, orgID)
	if err != nil {
		return nil, fmt.Errorf("list submissions: %w", err)
	}
	defer rows.Close()

	out := []Artifact{}
	for rows.Next() {
		var a Artifact
		var raw []byte
		if err := rows.Scan(&a.ID, &a.StageID, &a.Version, &a.Filename, &a.ByteSize,
			&a.Pages, &a.IsLate, &a.LockedAt, &a.SubmittedAt, &a.UploadedBy, &raw); err != nil {
			return nil, fmt.Errorf("scan submission: %w", err)
		}
		if report, err := unmarshalReport(raw); err == nil && report != nil {
			a.Compliance = report
			a.WordCount = report.WordCount
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Open streams a stored submission. The caller has already been authorised.
func (s *Store) Open(ctx context.Context, orgID, artifactID uuid.UUID) (io.ReadCloser, string, string, error) {
	var storageKey, filename, contentType string
	err := s.pool.QueryRow(ctx, `
		SELECT d.storage_key, d.filename, d.content_type
		FROM artifacts a
		JOIN documents d ON d.id = a.document_id
		WHERE a.id = $1 AND a.organization_id = $2`, artifactID, orgID).
		Scan(&storageKey, &filename, &contentType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", "", httpx.ErrNotFound()
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("load submission: %w", err)
	}

	rc, err := s.blobs.Get(ctx, storageKey)
	if errors.Is(err, blob.ErrNotFound) {
		return nil, "", "", httpx.ErrNotFound()
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("open submission blob: %w", err)
	}
	return rc, filename, contentType, nil
}

// TeamOf returns the team that owns an assignment, for ownership checks.
func (s *Store) TeamOf(ctx context.Context, orgID, assignmentID uuid.UUID) (uuid.UUID, error) {
	var teamID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT team_id FROM assignments WHERE id = $1 AND organization_id = $2`,
		assignmentID, orgID).Scan(&teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, httpx.ErrNotFound()
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("load assignment team: %w", err)
	}
	return teamID, nil
}

func (s *Store) TeamOfArtifact(ctx context.Context, orgID, artifactID uuid.UUID) (uuid.UUID, error) {
	var teamID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT a.team_id FROM artifacts art
		JOIN assignments a ON a.id = art.assignment_id
		WHERE art.id = $1 AND art.organization_id = $2`, artifactID, orgID).Scan(&teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, httpx.ErrNotFound()
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("load artifact team: %w", err)
	}
	return teamID, nil
}

// sniff identifies a format from leading bytes, mirroring the AI plane's
// check. Defence in depth: the control plane decides what it is willing to
// store before anything else sees it.
func sniff(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "pdf", nil
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return "docx", nil
	case len(data) > 0 && !bytes.Contains(data[:min(len(data), 4096)], []byte{0}):
		return "txt", nil
	default:
		return "", errors.New("unrecognised format")
	}
}

func allowed(formats []string, format string) bool {
	for _, f := range formats {
		if f == format {
			return true
		}
		// Plain-text rule sets accept markdown too.
		if f == "md" && format == "txt" {
			return true
		}
	}
	return false
}

func contentTypeFor(format string) string {
	switch format {
	case "pdf":
		return "application/pdf"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	default:
		return "text/plain; charset=utf-8"
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type Handlers struct {
	store *Store
	audit *audit.Logger
}

func NewHandlers(store *Store, auditLog *audit.Logger) *Handlers {
	return &Handlers{store: store, audit: auditLog}
}

// maxUploadBytes bounds what the server will read before the template's own
// limit is consulted. The template limit is the real rule; this stops a
// request from costing memory before validation runs.
const maxUploadBytes = 55 << 20

func (h *Handlers) Upload(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	assignmentID, err := uuid.Parse(chi.URLParam(r, "assignmentID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid assignment id."))
		return
	}
	stageID := chi.URLParam(r, "stageID")
	if stageID == "" {
		httpx.Fail(w, r, httpx.ErrBadRequest("Stage id is required."))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httpx.Fail(w, r, httpx.Err(http.StatusRequestEntityTooLarge, "upload_too_large",
			"That upload is too large or not a valid multipart form."))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("A file field is required."))
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes))
	if err != nil {
		httpx.Fail(w, r, fmt.Errorf("read upload: %w", err))
		return
	}

	artifact, err := h.store.Upload(r.Context(), UploadInput{
		OrganizationID: p.OrganizationID,
		ActorID:        p.UserID,
		AssignmentID:   assignmentID,
		StageID:        stageID,
		Filename:       safeFilename(header.Filename),
		Data:           data,
	})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.audit.Record(r.Context(), audit.Entry{
		OrganizationID: &p.OrganizationID, ActorUserID: &p.UserID,
		Action: "submission.upload", TargetKind: "artifact", TargetID: &artifact.ID,
		After: map[string]any{
			"assignment_id": assignmentID, "stage_id": stageID,
			"version": artifact.Version, "bytes": artifact.ByteSize,
			"is_late": artifact.IsLate, "locked": artifact.LockedAt != nil,
		},
		RequestID: httpx.RequestIDFrom(r.Context()),
	})

	httpx.JSON(w, r, http.StatusCreated, artifact)
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	assignmentID, err := uuid.Parse(chi.URLParam(r, "assignmentID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid assignment id."))
		return
	}

	if err := h.authorise(r, p, func() (uuid.UUID, error) {
		return h.store.TeamOf(r.Context(), p.OrganizationID, assignmentID)
	}); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out, err := h.store.List(r.Context(), p.OrganizationID, assignmentID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"submissions": out})
}

func (h *Handlers) Download(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	artifactID, err := uuid.Parse(chi.URLParam(r, "artifactID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid submission id."))
		return
	}

	if err := h.authorise(r, p, func() (uuid.UUID, error) {
		return h.store.TeamOfArtifact(r.Context(), p.OrganizationID, artifactID)
	}); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	rc, filename, contentType, err := h.store.Open(r.Context(), p.OrganizationID, artifactID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, safeFilename(filename)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, rc); err != nil {
		httpx.LoggerFrom(r.Context()).Error("stream submission", "error", err)
	}
}

// authorise lets staff with the organisation-wide permission through, and
// otherwise requires the caller to be on the owning team.
func (h *Handlers) authorise(r *http.Request, p auth.Principal, team func() (uuid.UUID, error)) error {
	if p.Can("submission.view") {
		return nil
	}
	if !p.Can("submission.view_own") {
		return httpx.ErrForbidden()
	}
	teamID, err := team()
	if err != nil {
		return err
	}
	member, err := h.store.isTeamMember(r.Context(), teamID, p.UserID)
	if err != nil {
		return err
	}
	if !member {
		return httpx.ErrNotFound()
	}
	return nil
}

// safeFilename strips path components and control characters so a hostile
// filename cannot influence storage paths or response headers.
func safeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "submission"
	}
	if len(name) > 180 {
		name = name[:180]
	}
	return name
}
