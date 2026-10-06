package seeddata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/intelimek/megamoot/apps/api/internal/auth"
)

// DemoTeamName is the team the seeded students belong to. It matches the
// two-speaker shape the moot template expects, so it can be assigned without
// any further setup.
const DemoTeamName = "Demo Team"

type person struct {
	Email    string
	FullName string
	Role     string
	// Speaking order for team members; zero means not a speaker.
	SpeakingOrder int
}

var demoPeople = []person{
	{Email: "teacher@demo.test", FullName: "Rohan Mehta", Role: "teacher"},
	{Email: "student1@demo.test", FullName: "Ananya Iyer", Role: "student", SpeakingOrder: 1},
	{Email: "student2@demo.test", FullName: "Devika Nair", Role: "student", SpeakingOrder: 2},
}

// SeedPeople creates a demo teacher, two students and a team of the right
// shape, so the product can be signed into at every permission level without
// clicking through user administration first.
//
// Idempotent, and it never resets an existing password. Skipped entirely when
// no password is configured.
func SeedPeople(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID,
	password string, log *slog.Logger) error {

	if password == "" {
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin seed people: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash demo password: %w", err)
	}

	ids := make(map[string]uuid.UUID, len(demoPeople))
	created := 0

	for _, p := range demoPeople {
		userID, isNew, err := upsertPerson(ctx, tx, orgID, p, hash)
		if err != nil {
			return err
		}
		ids[p.Email] = userID
		if isNew {
			created++
		}
	}

	if err := upsertDemoTeam(ctx, tx, orgID, ids); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit seed people: %w", err)
	}

	if created > 0 {
		log.Warn("seed: demo accounts created with a shared password; change them before a pilot",
			"count", created, "emails", emails())
	}
	return nil
}

func emails() []string {
	out := make([]string, 0, len(demoPeople))
	for _, p := range demoPeople {
		out = append(out, p.Email)
	}
	return out
}

func upsertPerson(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, p person,
	hash string) (uuid.UUID, bool, error) {

	normalized := auth.NormalizeEmail(p.Email)

	var userID uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM users WHERE organization_id = $1 AND email_normalized = $2`,
		orgID, normalized).Scan(&userID)

	isNew := false
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (organization_id, email, email_normalized, full_name,
			                   password_hash, status)
			VALUES ($1, $2, $3, $4, $5, 'active')
			RETURNING id`,
			orgID, strings.TrimSpace(p.Email), normalized, p.FullName, hash,
		).Scan(&userID); err != nil {
			return uuid.Nil, false, fmt.Errorf("create demo user %s: %w", p.Email, err)
		}
		isNew = true
	case err != nil:
		return uuid.Nil, false, fmt.Errorf("look up demo user %s: %w", p.Email, err)
	}

	// Granting the role is separate from creating the user so an account whose
	// role was removed by hand gets it back on the next start.
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		SELECT $1, r.id FROM roles r
		WHERE r.organization_id = $2 AND r.key = $3
		ON CONFLICT DO NOTHING`, userID, orgID, p.Role); err != nil {
		return uuid.Nil, false, fmt.Errorf("grant %s role to %s: %w", p.Role, p.Email, err)
	}

	return userID, isNew, nil
}

func upsertDemoTeam(ctx context.Context, tx pgx.Tx, orgID uuid.UUID,
	ids map[string]uuid.UUID) error {

	var teamID uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM teams WHERE organization_id = $1 AND name = $2`,
		orgID, DemoTeamName).Scan(&teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx,
			`INSERT INTO teams (organization_id, name) VALUES ($1, $2) RETURNING id`,
			orgID, DemoTeamName).Scan(&teamID); err != nil {
			return fmt.Errorf("create demo team: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("look up demo team: %w", err)
	}

	for _, p := range demoPeople {
		if p.SpeakingOrder == 0 {
			continue
		}
		userID, ok := ids[p.Email]
		if !ok {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO team_members (team_id, user_id, role, speaking_order)
			VALUES ($1, $2, 'speaker', $3)
			ON CONFLICT (team_id, user_id) DO NOTHING`,
			teamID, userID, p.SpeakingOrder); err != nil {
			return fmt.Errorf("add %s to the demo team: %w", p.Email, err)
		}
	}
	return nil
}
