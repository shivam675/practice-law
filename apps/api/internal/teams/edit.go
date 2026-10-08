package teams

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/intelimek/megamoot/apps/api/internal/audit"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/jackc/pgx/v5"
	"net/http"
)

func sameMembers(a, b []MemberInput) bool {
	if len(a) != len(b) {
		return false
	}
	byID := map[uuid.UUID]MemberInput{}
	for _, m := range a {
		byID[m.UserID] = m
	}
	for _, m := range b {
		old, ok := byID[m.UserID]
		if !ok || old.Role != m.Role || (old.SpeakingOrder == nil) != (m.SpeakingOrder == nil) {
			return false
		}
		if old.SpeakingOrder != nil && *old.SpeakingOrder != *m.SpeakingOrder {
			return false
		}
	}
	return true
}

func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, in CreateInput) error {
	if err := in.validate(); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin team edit: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var locked uuid.UUID
	// Assignment creation takes a share lock before measuring membership.
	err = tx.QueryRow(ctx, `SELECT id FROM teams WHERE id=$1 AND organization_id=$2 FOR UPDATE`, id, orgID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound()
	}
	if err != nil {
		return fmt.Errorf("lock team: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT m.user_id,m.role,m.speaking_order FROM team_members m JOIN teams t ON t.id=m.team_id WHERE t.id=$1 AND t.organization_id=$2`, id, orgID)
	if err != nil {
		return fmt.Errorf("read team membership: %w", err)
	}
	current := []MemberInput{}
	for rows.Next() {
		var m MemberInput
		if err = rows.Scan(&m.UserID, &m.Role, &m.SpeakingOrder); err != nil {
			rows.Close()
			return err
		}
		current = append(current, m)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if !sameMembers(current, in.Members) {
		var assigned bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assignments WHERE team_id=$1 AND organization_id=$2)`, id, orgID).Scan(&assigned); err != nil {
			return err
		}
		if assigned {
			return httpx.ErrConflict("This team has assessment history. Create a new team to change its membership or speaking order.")
		}
		if _, err = tx.Exec(ctx, `DELETE FROM team_members m USING teams t WHERE m.team_id=t.id AND t.id=$1 AND t.organization_id=$2`, id, orgID); err != nil {
			return err
		}
		for _, m := range in.Members {
			tag, e := tx.Exec(ctx, `INSERT INTO team_members(team_id,user_id,role,speaking_order) SELECT $1,id,$3,$4 FROM users WHERE id=$2 AND organization_id=$5 AND status<>'deleted'`, id, m.UserID, m.Role, m.SpeakingOrder, orgID)
			if e != nil {
				return fmt.Errorf("update member: %w", e)
			}
			if tag.RowsAffected() != 1 {
				return httpx.ErrBadRequest("A selected member is not available in this organisation.")
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE teams SET name=$3 WHERE id=$1 AND organization_id=$2`, id, orgID, in.Name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	if !p.Can("team.edit") {
		httpx.Fail(w, r, httpx.ErrForbidden())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "teamID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid team id."))
		return
	}
	var in CreateInput
	if err = httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err = h.store.Update(r.Context(), p.OrganizationID, id, in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	h.audit.Record(r.Context(), audit.Entry{OrganizationID: &p.OrganizationID, ActorUserID: &p.UserID, Action: "team.edit", TargetKind: "team", TargetID: &id, After: map[string]any{"name": in.Name, "members": len(in.Members)}, RequestID: httpx.RequestIDFrom(r.Context())})
	httpx.JSON(w, r, http.StatusOK, map[string]any{"id": id})
}
