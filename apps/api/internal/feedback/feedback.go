// Package feedback records what a student thought of an oral round: whether
// the bench asked about their case, how often it cut in, and what broke. It is
// evidence for tuning the judge and never an input to a grade.
package feedback

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slmlabs/megamoot/apps/api/internal/auth"
	"github.com/slmlabs/megamoot/apps/api/internal/httpx"
	"github.com/slmlabs/megamoot/apps/api/internal/workflow"
)

type Handlers struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Handlers { return &Handlers{pool} }

type Input struct {
	Rating        int      `json:"rating"`
	Relevance     string   `json:"relevance"`
	Interruptions string   `json:"interruptions"`
	Problems      []string `json:"problems"`
	Comment       string   `json:"comment"`
}

type Entry struct {
	ID           uuid.UUID `json:"id"`
	SessionID    uuid.UUID `json:"session_id"`
	AssignmentID uuid.UUID `json:"assignment_id"`
	StageID      string    `json:"stage_id"`
	Student      string    `json:"student"`
	Assessment   string    `json:"assessment_title"`
	Input
	CreatedAt time.Time `json:"created_at"`
}

var problemKinds = map[string]bool{"audio": true, "transcript": true, "judge_silent": true, "judge_repeated": true, "other": true}

// validate mirrors the table's checks so a bad form gets a sentence rather
// than a constraint violation, and normalises what it accepts.
func (in *Input) validate() error {
	if in.Rating < 1 || in.Rating > 5 {
		return httpx.ErrBadRequest("Choose a rating from 1 to 5.")
	}
	switch in.Relevance {
	case "yes", "partly", "no":
	default:
		return httpx.ErrBadRequest("Say whether the judge's questions were about your case.")
	}
	switch in.Interruptions {
	case "about_right", "too_many", "too_few":
	default:
		return httpx.ErrBadRequest("Say how often the judge interrupted.")
	}
	seen := map[string]bool{}
	for _, p := range in.Problems {
		if !problemKinds[p] || seen[p] {
			return httpx.ErrBadRequest("Choose problems from the list, each once.")
		}
		seen[p] = true
	}
	if in.Problems == nil {
		in.Problems = []string{} // the column is NOT NULL; nil would encode as NULL
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if utf8.RuneCountInString(in.Comment) > 2000 {
		return httpx.ErrBadRequest("Keep the comment under 2000 characters.")
	}
	return nil
}

// Submit saves or replaces the caller's feedback on a round they spoke in.
func (h *Handlers) Submit(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "sessionID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrNotFound())
		return
	}
	var in Input
	if err = httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err = in.validate(); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	// Only a human who took part may answer. Anyone else gets the same 404 as
	// a session that does not exist.
	var status string
	err = h.pool.QueryRow(r.Context(), `SELECT s.status FROM sessions s WHERE s.id=$1 AND s.organization_id=$2 AND EXISTS(SELECT 1 FROM session_participants p WHERE p.session_id=s.id AND p.organization_id=s.organization_id AND p.kind='human' AND p.user_id=$3)`, id, p.OrganizationID, p.UserID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Fail(w, r, httpx.ErrNotFound())
		return
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if status == workflow.SessionRunning {
		httpx.Fail(w, r, httpx.ErrConflict("Feedback opens when the round ends."))
		return
	}
	_, err = h.pool.Exec(r.Context(), `INSERT INTO session_feedback(organization_id,session_id,user_id,rating,relevance,interruptions,problems,comment) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
 ON CONFLICT (session_id,user_id) DO UPDATE SET rating=EXCLUDED.rating,relevance=EXCLUDED.relevance,interruptions=EXCLUDED.interruptions,problems=EXCLUDED.problems,comment=EXCLUDED.comment`,
		p.OrganizationID, id, p.UserID, in.Rating, in.Relevance, in.Interruptions, in.Problems, in.Comment)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, 200, map[string]bool{"saved": true})
}

// Mine returns the caller's own answer, or null, so the form knows whether to
// say thanks.
func (h *Handlers) Mine(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "sessionID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrNotFound())
		return
	}
	var in Input
	err = h.pool.QueryRow(r.Context(), `SELECT rating,relevance,interruptions,problems,comment FROM session_feedback WHERE session_id=$1 AND organization_id=$2 AND user_id=$3`, id, p.OrganizationID, p.UserID).Scan(&in.Rating, &in.Relevance, &in.Interruptions, &in.Problems, &in.Comment)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.JSON(w, r, 200, map[string]any{"feedback": nil})
		return
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, 200, map[string]any{"feedback": in})
}

// List is the organisation's feedback, newest first. The summary covers every
// row, not only the 200 shown.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	org := auth.MustPrincipal(r.Context()).OrganizationID
	var summary json.RawMessage
	err := h.pool.QueryRow(r.Context(), `SELECT json_build_object(
 'count', count(*), 'average_rating', round(avg(rating), 2),
 'relevance', json_build_object('yes', count(*) FILTER (WHERE relevance='yes'), 'partly', count(*) FILTER (WHERE relevance='partly'), 'no', count(*) FILTER (WHERE relevance='no')),
 'interruptions', json_build_object('about_right', count(*) FILTER (WHERE interruptions='about_right'), 'too_many', count(*) FILTER (WHERE interruptions='too_many'), 'too_few', count(*) FILTER (WHERE interruptions='too_few')))
 FROM session_feedback WHERE organization_id=$1`, org).Scan(&summary)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	rows, err := h.pool.Query(r.Context(), `SELECT f.id,f.session_id,s.assignment_id,s.stage_id,u.full_name,ass.title,f.rating,f.relevance,f.interruptions,f.problems,f.comment,f.created_at
 FROM session_feedback f
 JOIN sessions s ON s.id=f.session_id AND s.organization_id=f.organization_id
 JOIN assignments a ON a.id=s.assignment_id AND a.organization_id=f.organization_id
 JOIN assessments ass ON ass.id=a.assessment_id AND ass.organization_id=f.organization_id
 JOIN users u ON u.id=f.user_id AND u.organization_id=f.organization_id
 WHERE f.organization_id=$1 ORDER BY f.created_at DESC LIMIT 200`, org)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err = rows.Scan(&e.ID, &e.SessionID, &e.AssignmentID, &e.StageID, &e.Student, &e.Assessment, &e.Rating, &e.Relevance, &e.Interruptions, &e.Problems, &e.Comment, &e.CreatedAt); err != nil {
			httpx.Fail(w, r, err)
			return
		}
		out = append(out, e)
	}
	if err = rows.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, 200, map[string]any{"summary": summary, "feedback": out})
}
