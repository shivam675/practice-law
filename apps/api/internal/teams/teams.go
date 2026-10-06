// Package teams owns participant groupings.
//
// A team always exists, even for a solo candidate, which removes the
// individual-versus-team dual code path from grading, reporting and session
// participation. See ADR 0004.
package teams

import (
	"context"
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
)

const (
	RoleSpeaker    = "speaker"
	RoleResearcher = "researcher"
)

type Member struct {
	UserID        uuid.UUID `json:"user_id"`
	FullName      string    `json:"full_name"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	SpeakingOrder *int      `json:"speaking_order"`
}

type Team struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Members   []Member  `json:"members"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Team, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.name, t.created_at,
		       u.id, u.full_name, u.email, m.role, m.speaking_order
		FROM teams t
		LEFT JOIN team_members m ON m.team_id = t.id
		LEFT JOIN users u ON u.id = m.user_id
		WHERE t.organization_id = $1
		ORDER BY t.created_at DESC, m.speaking_order NULLS LAST, u.full_name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()

	var out []Team
	index := map[uuid.UUID]int{}

	for rows.Next() {
		var t Team
		var userID *uuid.UUID
		var fullName, email, role *string
		var speakingOrder *int

		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt,
			&userID, &fullName, &email, &role, &speakingOrder); err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}

		pos, ok := index[t.ID]
		if !ok {
			t.Members = []Member{}
			out = append(out, t)
			pos = len(out) - 1
			index[t.ID] = pos
		}
		if userID == nil {
			continue
		}
		out[pos].Members = append(out[pos].Members, Member{
			UserID: *userID, FullName: *fullName, Email: *email,
			Role: *role, SpeakingOrder: speakingOrder,
		})
	}
	return out, rows.Err()
}

type CreateInput struct {
	Name    string        `json:"name"`
	Members []MemberInput `json:"members"`
}

type MemberInput struct {
	UserID        uuid.UUID `json:"user_id"`
	Role          string    `json:"role"`
	SpeakingOrder *int      `json:"speaking_order"`
}

func (in CreateInput) validate() error {
	var problems []string

	if strings.TrimSpace(in.Name) == "" {
		problems = append(problems, "name is required")
	}
	if len(in.Members) == 0 {
		problems = append(problems, "a team needs at least one member")
	}

	seenUsers := map[uuid.UUID]struct{}{}
	seenOrder := map[int]struct{}{}
	for i, m := range in.Members {
		where := fmt.Sprintf("members[%d]", i)
		if _, dup := seenUsers[m.UserID]; dup {
			problems = append(problems, where+": duplicate user")
		}
		seenUsers[m.UserID] = struct{}{}

		switch m.Role {
		case RoleSpeaker:
			if m.SpeakingOrder == nil || *m.SpeakingOrder < 1 {
				problems = append(problems, where+": a speaker needs a speaking_order of 1 or more")
				continue
			}
			if _, dup := seenOrder[*m.SpeakingOrder]; dup {
				problems = append(problems, fmt.Sprintf(
					"%s: speaking_order %d is already taken", where, *m.SpeakingOrder))
			}
			seenOrder[*m.SpeakingOrder] = struct{}{}
		case RoleResearcher:
			if m.SpeakingOrder != nil {
				problems = append(problems, where+": a researcher cannot hold a speaking_order")
			}
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown role %q", where, m.Role))
		}
	}

	// Speaking order must be 1..n with no gaps, or stage speaker_order
	// references point at a speaker who does not exist.
	for i := 1; i <= len(seenOrder); i++ {
		if _, ok := seenOrder[i]; !ok {
			problems = append(problems, fmt.Sprintf(
				"speaking order must run 1 to %d without gaps; %d is missing", len(seenOrder), i))
			break
		}
	}

	if len(problems) > 0 {
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_team",
			"The team is not valid.").WithFields(map[string]any{"problems": problems})
	}
	return nil
}

func (s *Store) Create(ctx context.Context, orgID, actorID uuid.UUID, in CreateInput) (Team, error) {
	if err := in.validate(); err != nil {
		return Team{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Team{}, fmt.Errorf("begin create team: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	out := Team{Name: in.Name, Members: []Member{}}
	if err := tx.QueryRow(ctx, `
		INSERT INTO teams (organization_id, name, created_by)
		VALUES ($1, $2, $3) RETURNING id, created_at`,
		orgID, in.Name, actorID).Scan(&out.ID, &out.CreatedAt); err != nil {
		return Team{}, fmt.Errorf("create team: %w", err)
	}

	for _, m := range in.Members {
		member := Member{UserID: m.UserID, Role: m.Role, SpeakingOrder: m.SpeakingOrder}

		// Resolving the user inside the tenant is what enforces isolation: an
		// id from another organisation simply matches no row.
		err := tx.QueryRow(ctx, `
			SELECT full_name, email FROM users
			WHERE id = $1 AND organization_id = $2 AND status <> 'deleted'`,
			m.UserID, orgID).Scan(&member.FullName, &member.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			return Team{}, httpx.ErrBadRequest(
				fmt.Sprintf("User %s does not exist in this organisation.", m.UserID))
		}
		if err != nil {
			return Team{}, fmt.Errorf("resolve team member: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO team_members (team_id, user_id, role, speaking_order)
			VALUES ($1, $2, $3, $4)`,
			out.ID, m.UserID, m.Role, m.SpeakingOrder); err != nil {
			return Team{}, fmt.Errorf("add team member: %w", err)
		}
		out.Members = append(out.Members, member)
	}

	if err := tx.Commit(ctx); err != nil {
		return Team{}, fmt.Errorf("commit create team: %w", err)
	}
	return out, nil
}

// Size returns the member count and speaker count, which assignment creation
// checks against the template's participation shape.
func (s *Store) Size(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, orgID, teamID uuid.UUID) (members, speakers int, err error) {

	err = q.QueryRow(ctx, `
		SELECT count(*)::int,
		       count(*) FILTER (WHERE m.role = 'speaker')::int
		FROM team_members m
		JOIN teams t ON t.id = m.team_id
		WHERE m.team_id = $1 AND t.organization_id = $2`, teamID, orgID).
		Scan(&members, &speakers)
	if err != nil {
		return 0, 0, fmt.Errorf("measure team: %w", err)
	}
	return members, speakers, nil
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
	httpx.JSON(w, r, http.StatusOK, map[string]any{"teams": out})
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
		Action: "team.create", TargetKind: "team", TargetID: &out.ID,
		After:     map[string]any{"name": out.Name, "members": len(out.Members)},
		RequestID: httpx.RequestIDFrom(r.Context()),
	})
	httpx.JSON(w, r, http.StatusCreated, out)
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "teamID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid team id."))
		return
	}
	all, err := h.store.List(r.Context(), p.OrganizationID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	for _, t := range all {
		if t.ID == id {
			httpx.JSON(w, r, http.StatusOK, t)
			return
		}
	}
	httpx.Fail(w, r, httpx.ErrNotFound())
}
