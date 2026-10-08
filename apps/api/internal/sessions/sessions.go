package sessions

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/harness"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/intelimek/megamoot/apps/api/internal/spec"
	"github.com/intelimek/megamoot/apps/api/internal/workflow"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handlers struct {
	pool     *pgxpool.Pool
	engine   *workflow.Engine
	harness  *harness.Harness
	secret   []byte
	capacity int
}

func New(pool *pgxpool.Pool, engine *workflow.Engine, h *harness.Harness, secret string, capacity int) *Handlers {
	return &Handlers{pool, engine, h, []byte(secret), capacity}
}

type Claims struct {
	SessionID uuid.UUID `json:"session_id"`
	OrgID     uuid.UUID `json:"organization_id"`
	jwt.RegisteredClaims
}
type Session struct {
	ID         uuid.UUID  `json:"id"`
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"started_at"`
	EndsAt     *time.Time `json:"ends_at"`
	Transcript []Turn     `json:"transcript"`
}
type Turn struct {
	Seq     int64  `json:"seq"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

func (h *Handlers) Join(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	var input struct {
		Consent bool `json:"consent"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if !input.Consent {
		httpx.Fail(w, r, httpx.ErrBadRequest("Consent to transcription and AI assessment is required."))
		return
	}
	assignment, err := uuid.Parse(chi.URLParam(r, "assignmentID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid assignment."))
		return
	}
	stage := chi.URLParam(r, "stageID")
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(r.Context()))
	var raw []byte
	var status string
	var speakerOrder *int
	var stageRow uuid.UUID
	err = tx.QueryRow(r.Context(), `SELECT st.id,st.status,v.value->'config',m.speaking_order
 FROM assignments a JOIN assignment_stages st ON st.assignment_id=a.id AND st.organization_id=a.organization_id
 JOIN assessments ass ON ass.id=a.assessment_id JOIN assessment_template_versions tv ON tv.id=ass.template_version_id
 JOIN team_members m ON m.team_id=a.team_id AND m.user_id=$3 AND m.role='speaker'
 CROSS JOIN LATERAL jsonb_array_elements(tv.stages) v(value)
 WHERE a.id=$1 AND a.organization_id=$2 AND st.stage_id=$4 AND st.stage_kind='live_turn'
 AND v.value->>'id'=st.stage_id AND a.status IN ('assigned','in_progress') FOR UPDATE OF st`, assignment, p.OrganizationID, p.UserID, stage).Scan(&stageRow, &status, &raw, &speakerOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Fail(w, r, httpx.ErrNotFound())
		return
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if status != workflow.StageActive {
		httpx.Fail(w, r, httpx.ErrConflict("This speaking stage is not open."))
		return
	}
	var cfg spec.LiveTurnConfig
	if err = json.Unmarshal(raw, &cfg); err != nil || cfg.DurationS <= 0 {
		httpx.Fail(w, r, httpx.ErrConflict("The speaking stage is not configured."))
		return
	}
	if cfg.SpeakerOrder > 0 && (speakerOrder == nil || *speakerOrder != cfg.SpeakerOrder) {
		httpx.Fail(w, r, httpx.ErrForbidden())
		return
	}
	// Serialize admission with other joins across API instances.
	if _, err = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(672042)`); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var sessionID uuid.UUID
	err = tx.QueryRow(r.Context(), `SELECT id FROM sessions WHERE assignment_id=$1 AND stage_id=$2 AND organization_id=$3`, assignment, stage, p.OrganizationID).Scan(&sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		var active int
		if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM sessions WHERE status IN ('running','paused','degraded')`).Scan(&active); err != nil {
			httpx.Fail(w, r, err)
			return
		}
		if active >= h.capacity {
			httpx.Fail(w, r, httpx.ErrConflict("All speaking rooms are busy. Retry shortly."))
			return
		}
		err = tx.QueryRow(r.Context(), `INSERT INTO sessions(organization_id,assignment_id,stage_id,scheduled_at) VALUES($1,$2,$3,now()) RETURNING id`, p.OrganizationID, assignment, stage).Scan(&sessionID)
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO session_participants(organization_id,session_id,kind,role,ai_profile_id,display_name,is_presiding,joined_at)
 SELECT $1,$2,'ai',slot->>'role',p.id,slot->>'display_name',coalesce((slot->>'presiding')::boolean,false),now()
 FROM assignments a JOIN assessments ass ON ass.id=a.assessment_id AND ass.organization_id=a.organization_id
 JOIN assessment_template_versions tv ON tv.id=ass.template_version_id
 CROSS JOIN LATERAL jsonb_array_elements(tv.participation->'ai_actors') slot
 JOIN LATERAL (SELECT id FROM ai_profiles WHERE organization_id=$1 AND key=slot->>'profile_key' AND is_active ORDER BY version DESC LIMIT 1) p ON true
 WHERE a.id=$3 AND a.organization_id=$1`, p.OrganizationID, sessionID, assignment)
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
		for _, to := range []string{workflow.SessionLobby, workflow.SessionDeviceCheck, workflow.SessionRunning} {
			_, err = h.engine.ApplyTx(r.Context(), tx, workflow.Request{Subject: workflow.SubjectSession, SubjectID: sessionID, OrganizationID: p.OrganizationID, To: to, Cause: "participant_joined", Actor: workflow.UserActor(p.UserID)})
			if err != nil {
				httpx.Fail(w, r, err)
				return
			}
		}
		// Capture stops at the speaking deadline; allow buffered inference to drain.
		if err = workflow.Schedule(r.Context(), tx, p.OrganizationID, workflow.SubjectSession, sessionID, workflow.SessionEnded, "time_elapsed", "drain_end", time.Now().Add(time.Duration(cfg.DurationS+120)*time.Second)); err != nil {
			httpx.Fail(w, r, err)
			return
		}
	} else if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var running string
	if err = tx.QueryRow(r.Context(), `SELECT status FROM sessions WHERE id=$1 AND organization_id=$2`, sessionID, p.OrganizationID).Scan(&running); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if running != workflow.SessionRunning {
		httpx.Fail(w, r, httpx.ErrConflict("This session has ended."))
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO session_participants(organization_id,session_id,kind,role,user_id,display_name,joined_at)
 SELECT $1,$2,'human','candidate',u.id,u.full_name,now() FROM users u WHERE u.id=$3 AND u.organization_id=$1
 AND NOT EXISTS(SELECT 1 FROM session_participants WHERE session_id=$2 AND user_id=$3)`, p.OrganizationID, sessionID, p.UserID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO consent_records(organization_id,user_id,scope,granted) VALUES($1,$2,'transcript_retention',true),($1,$2,'ai_evaluation',true)`, p.OrganizationID, p.UserID); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	claims := Claims{SessionID: sessionID, OrgID: p.OrganizationID, RegisteredClaims: jwt.RegisteredClaims{Subject: p.UserID.String(), Audience: jwt.ClaimStrings{"megamoot-media"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(cfg.DurationS+300) * time.Second))}}
	ticket, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(h.secret)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, 200, map[string]any{"id": sessionID, "ticket": ticket, "speech_path": "/speech"})
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "sessionID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrNotFound())
		return
	}
	var allowed bool
	err = h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM sessions s JOIN assignments a ON a.id=s.assignment_id WHERE s.id=$1 AND s.organization_id=$2 AND ($4 OR EXISTS(SELECT 1 FROM team_members m WHERE m.team_id=a.team_id AND m.user_id=$3)))`, id, p.OrganizationID, p.UserID, p.Can("session.observe")).Scan(&allowed)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if !allowed {
		httpx.Fail(w, r, httpx.ErrNotFound())
		return
	}
	h.respond(w, r, id, p.OrganizationID)
}
func (h *Handlers) respond(w http.ResponseWriter, r *http.Request, id, org uuid.UUID) {
	out := Session{ID: id, Transcript: []Turn{}}
	err := h.pool.QueryRow(r.Context(), `SELECT s.status,s.started_at,(SELECT run_at-CASE WHEN cause_key='drain_end' THEN interval '120 seconds' ELSE interval '0 seconds' END FROM scheduled_transitions WHERE subject_kind='session' AND subject_id=s.id AND cause_key IN ('end','drain_end') LIMIT 1) FROM sessions s WHERE s.id=$1 AND s.organization_id=$2`, id, org).Scan(&out.Status, &out.StartedAt, &out.EndsAt)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	rows, err := h.pool.Query(r.Context(), `SELECT seq,coalesce(payload->>'speaker','Participant'),payload->>'text' FROM session_events WHERE session_id=$1 AND organization_id=$2 AND type IN ('transcript_final','judge_question') ORDER BY seq`, id, org)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var turn Turn
		if err = rows.Scan(&turn.Seq, &turn.Speaker, &turn.Text); err != nil {
			httpx.Fail(w, r, err)
			return
		}
		out.Transcript = append(out.Transcript, turn)
	}
	if err = rows.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, 200, out)
}
func (h *Handlers) End(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "sessionID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrNotFound())
		return
	}
	var allowed bool
	err = h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM sessions s WHERE s.id=$1 AND s.organization_id=$2 AND ($4 OR EXISTS(SELECT 1 FROM session_participants p WHERE p.session_id=s.id AND p.user_id=$3)))`, id, p.OrganizationID, p.UserID, p.Can("session.moderate")).Scan(&allowed)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if !allowed {
		httpx.Fail(w, r, httpx.ErrNotFound())
		return
	}
	err = h.engine.Apply(r.Context(), workflow.Request{Subject: workflow.SubjectSession, SubjectID: id, OrganizationID: p.OrganizationID, To: workflow.SessionEnded, Cause: "participant_finished", Actor: workflow.UserActor(p.UserID)})
	if err != nil && !errors.Is(err, workflow.ErrNoop) {
		httpx.Fail(w, r, err)
		return
	}
	h.respond(w, r, id, p.OrganizationID)
}
func (h *Handlers) claims(r *http.Request, draining bool) (*Claims, error) {
	c := &Claims{}
	_, err := jwt.ParseWithClaims(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), c, func(t *jwt.Token) (any, error) { return h.secret, nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("megamoot-media"), jwt.WithExpirationRequired())
	if err != nil {
		return nil, httpx.ErrUnauthorized()
	}
	var valid bool
	err = h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM sessions s JOIN session_participants p ON p.session_id=s.id WHERE s.id=$1 AND s.organization_id=$2 AND p.user_id=$3 AND s.status='running' AND NOT EXISTS(SELECT 1 FROM scheduled_transitions t WHERE t.subject_id=s.id AND t.cause_key IN ('end','drain_end') AND t.run_at-CASE WHEN t.cause_key='drain_end' AND NOT $4 THEN interval '120 seconds' ELSE interval '0 seconds' END<=now()))`, c.SessionID, c.OrgID, c.Subject, draining).Scan(&valid)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, httpx.ErrConflict("The session is no longer active.")
	}
	return c, nil
}
func (h *Handlers) Verify(w http.ResponseWriter, r *http.Request) {
	c, err := h.claims(r, false)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	h.respond(w, r, c.SessionID, c.OrgID)
}

// Finish is called by the trusted media service only after buffered audio is saved.
func (h *Handlers) Finish(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Media-Service-Token")), h.secret) != 1 {
		httpx.Fail(w, r, httpx.ErrUnauthorized())
		return
	}
	c, err := h.claims(r, true)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	err = h.engine.Apply(r.Context(), workflow.Request{Subject: workflow.SubjectSession, SubjectID: c.SessionID, OrganizationID: c.OrgID, To: workflow.SessionEnded, Cause: "speech_drained", Actor: workflow.SystemActor()})
	if err != nil && !errors.Is(err, workflow.ErrNoop) {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, 200, map[string]bool{"saved": true})
}
func (h *Handlers) Turn(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Media-Service-Token")), h.secret) != 1 {
		httpx.Fail(w, r, httpx.ErrUnauthorized())
		return
	}
	c, err := h.claims(r, true)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var in struct {
		ID         string `json:"id"`
		Text       string `json:"text"`
		DurationMS int    `json:"duration_ms"`
	}
	if err = httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if _, err = uuid.Parse(in.ID); err != nil || strings.TrimSpace(in.Text) == "" || len(in.Text) > 12000 || in.DurationMS < 0 || in.DurationMS > 30000 {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid transcript turn."))
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(r.Context()))
	var assignment uuid.UUID
	var stage string
	var started time.Time
	var seq int64
	err = tx.QueryRow(r.Context(), `SELECT assignment_id,stage_id,started_at,last_seq FROM sessions WHERE id=$1 AND organization_id=$2 AND status='running' FOR UPDATE`, c.SessionID, c.OrgID).Scan(&assignment, &stage, &started, &seq)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var duplicate bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM session_events WHERE session_id=$1 AND payload->>'client_id'=$2)`, c.SessionID, in.ID).Scan(&duplicate); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if duplicate {
		httpx.JSON(w, r, 200, map[string]any{"saved": true})
		return
	}
	var participant uuid.UUID
	var name string
	if err = tx.QueryRow(r.Context(), `SELECT id,display_name FROM session_participants WHERE session_id=$1 AND organization_id=$2 AND user_id=$3 LIMIT 1`, c.SessionID, c.OrgID, c.Subject).Scan(&participant, &name); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	payload, _ := json.Marshal(map[string]any{"client_id": in.ID, "speaker": name, "text": in.Text})
	offset := max(int64(0), time.Since(started).Milliseconds())
	seq++
	_, err = tx.Exec(r.Context(), `INSERT INTO session_events(session_id,seq,organization_id,type,actor_kind,actor_id,payload,offset_ms) VALUES($1,$2,$3,'transcript_final','human',$4,$5,$6)`, c.SessionID, seq, c.OrgID, c.Subject, payload, offset)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO transcript_segments(organization_id,session_id,participant_id,seq,start_ms,end_ms,text) VALUES($1,$2,$3,$4,$5,$6,$7)`, c.OrgID, c.SessionID, participant, seq, max(int64(0), offset-int64(in.DurationMS)), offset, in.Text)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE sessions SET last_seq=$2 WHERE id=$1 AND organization_id=$3`, c.SessionID, seq, c.OrgID)
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, 200, map[string]any{"saved": true})
}

func (h *Handlers) Question(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Media-Service-Token")), h.secret) != 1 {
		httpx.Fail(w, r, httpx.ErrUnauthorized())
		return
	}
	c, err := h.claims(r, false)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var assignment uuid.UUID
	var stage string
	err = h.pool.QueryRow(r.Context(), `SELECT assignment_id,stage_id FROM sessions WHERE id=$1 AND organization_id=$2`, c.SessionID, c.OrgID).Scan(&assignment, &stage)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	question, err := h.question(r.Context(), c, assignment, stage)
	if err != nil {
		httpx.LoggerFrom(r.Context()).Warn("judge unavailable", "error", err)
		httpx.JSON(w, r, 200, map[string]string{"warning": "The judge is unavailable. Your transcript was saved."})
		return
	}
	voice, name := "af_heart", "Examiner"
	if question != "" {
		if err = h.pool.QueryRow(r.Context(), `SELECT coalesce(payload->>'voice','af_heart'),coalesce(payload->>'speaker','Examiner') FROM session_events WHERE session_id=$1 AND organization_id=$2 AND type='judge_question' ORDER BY seq DESC LIMIT 1`, c.SessionID, c.OrgID).Scan(&voice, &name); err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}
	httpx.JSON(w, r, 200, map[string]string{"question": question, "voice": voice, "speaker": name})
}
