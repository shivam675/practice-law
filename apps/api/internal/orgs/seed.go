// Package orgs owns organisations and the bootstrap that makes a fresh
// database usable.
package orgs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/intelimek/megamoot/apps/api/internal/auth"
)

// systemRoles are seeded into every organisation. Custom roles can be added
// later; these cannot be deleted. Each is only a set of permissions.
var systemRoles = []struct {
	Key         string
	Name        string
	Description string
	Permissions []string
}{
	{
		Key: "student", Name: "Student",
		Description: "Participates in assessments",
		Permissions: []string{
			"assessment.view_own",
			"submission.upload", "submission.view_own",
			"report.view_own",
			"session.join", "session.view_transcript",
			"knowledge.view",
			"team.view",
		},
	},
	{
		Key: "teacher", Name: "Teacher",
		Description: "Creates, runs and grades assessments",
		Permissions: []string{
			"assessment.create", "assessment.edit", "assessment.publish",
			"assessment.assign", "assessment.view", "assessment.grade",
			"assessment.override_grade",
			"template.create", "template.edit", "template.view",
			"rubric.create", "rubric.edit", "rubric.view",
			"team.create", "team.edit", "team.view",
			"session.start", "session.observe", "session.moderate",
			"session.view_transcript",
			"submission.view", "submission.unlock",
			"report.view", "report.annotate", "report.publish",
			"knowledge.upload", "knowledge.view",
			"ai_profile.view",
			"user.view",
		},
	},
	{
		Key: "admin", Name: "Administrator",
		Description: "Manages the organisation",
		// Every non-platform permission; resolved at seed time.
		Permissions: nil,
	},
}

type BootstrapInput struct {
	OrgName       string
	AdminEmail    string
	AdminPassword string
}

// Bootstrap makes a fresh database usable: one organisation, the system roles
// with their permissions, and one administrator.
//
// It is idempotent. Existing rows are left alone, so it is safe to run on
// every start in development and harmless in production.
func Bootstrap(ctx context.Context, pool *pgxpool.Pool, in BootstrapInput, log *slog.Logger) (uuid.UUID, error) {
	if in.OrgName == "" {
		return uuid.Nil, nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	slug := Slugify(in.OrgName)

	var orgID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM organizations WHERE slug = $1`, slug).Scan(&orgID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := tx.QueryRow(ctx, `
			INSERT INTO organizations (slug, name) VALUES ($1, $2) RETURNING id`,
			slug, in.OrgName).Scan(&orgID); err != nil {
			return uuid.Nil, fmt.Errorf("create organization: %w", err)
		}
		log.Info("bootstrap: organization created", "slug", slug)
	case err != nil:
		return uuid.Nil, fmt.Errorf("look up organization: %w", err)
	}

	roleIDs, err := seedRoles(ctx, tx, orgID)
	if err != nil {
		return uuid.Nil, err
	}

	if in.AdminEmail == "" || in.AdminPassword == "" {
		log.Warn("bootstrap: no seed administrator configured; set SEED_ADMIN_EMAIL and SEED_ADMIN_PASSWORD")
		return orgID, tx.Commit(ctx)
	}

	adminRoleID, ok := roleIDs["admin"]
	if !ok {
		return uuid.Nil, fmt.Errorf("bootstrap: admin role missing after seeding")
	}

	created, err := seedAdmin(ctx, tx, orgID, adminRoleID, in)
	if err != nil {
		return uuid.Nil, err
	}
	if created {
		log.Info("bootstrap: administrator created", "email", in.AdminEmail, "organization", slug)
	}

	return orgID, tx.Commit(ctx)
}

func seedRoles(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (map[string]uuid.UUID, error) {
	// Resolve the admin role's permission set from the catalogue rather than
	// hardcoding it, so a new permission is granted to admins automatically.
	rows, err := tx.Query(ctx, `SELECT key FROM permissions WHERE NOT is_platform`)
	if err != nil {
		return nil, fmt.Errorf("load permission catalogue: %w", err)
	}
	var allOrgPermissions []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan permission key: %w", err)
		}
		allOrgPermissions = append(allOrgPermissions, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read permission catalogue: %w", err)
	}

	ids := make(map[string]uuid.UUID, len(systemRoles))
	for _, role := range systemRoles {
		var roleID uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO roles (organization_id, key, name, description, is_system)
			VALUES ($1, $2, $3, $4, true)
			ON CONFLICT (organization_id, key) WHERE organization_id IS NOT NULL
			DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description
			RETURNING id`,
			orgID, role.Key, role.Name, role.Description).Scan(&roleID)
		if err != nil {
			return nil, fmt.Errorf("upsert role %s: %w", role.Key, err)
		}
		ids[role.Key] = roleID

		perms := role.Permissions
		if perms == nil {
			perms = allOrgPermissions
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission_key)
			SELECT $1, unnest($2::text[])
			ON CONFLICT DO NOTHING`, roleID, perms); err != nil {
			return nil, fmt.Errorf("grant permissions to %s: %w", role.Key, err)
		}
	}

	return ids, nil
}

func seedAdmin(ctx context.Context, tx pgx.Tx, orgID, adminRoleID uuid.UUID, in BootstrapInput) (bool, error) {
	emailNorm := auth.NormalizeEmail(in.AdminEmail)

	var userID uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM users WHERE organization_id = $1 AND email_normalized = $2`,
		orgID, emailNorm).Scan(&userID)
	if err == nil {
		return false, nil // already present; never reset an existing password
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("look up seed administrator: %w", err)
	}

	hash, err := auth.HashPassword(in.AdminPassword)
	if err != nil {
		return false, fmt.Errorf("hash seed administrator password: %w", err)
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO users (organization_id, email, email_normalized, full_name,
		                   password_hash, status)
		VALUES ($1, $2, $3, $4, $5, 'active')
		RETURNING id`,
		orgID, strings.TrimSpace(in.AdminEmail), emailNorm, "Administrator", hash,
	).Scan(&userID); err != nil {
		return false, fmt.Errorf("create seed administrator: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, adminRoleID); err != nil {
		return false, fmt.Errorf("grant admin role: %w", err)
	}

	return true, nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonSlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "org"
	}
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	return s
}
