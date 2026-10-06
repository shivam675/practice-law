package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockKey serialises migration runs across API replicas. Two instances
// starting at once must not race the same CREATE TABLE.
const advisoryLockKey int64 = 7_312_004_119

// Migrate applies every unapplied .sql file from src in filename order.
//
// Each file runs inside its own transaction, so a failure leaves the schema at
// the last complete migration. Applied files are checksummed: editing a
// migration that already ran is an error, not a silent no-op.
func Migrate(ctx context.Context, pool *pgxpool.Pool, src fs.FS, log *slog.Logger) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx),
			`SELECT pg_advisory_unlock($1)`, advisoryLockKey); err != nil {
			log.Error("release migration advisory lock", "error", err)
		}
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    text PRIMARY KEY,
			checksum   text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := loadApplied(ctx, conn.Conn())
	if err != nil {
		return err
	}

	names, err := sqlFiles(src)
	if err != nil {
		return err
	}

	for _, name := range names {
		body, err := fs.ReadFile(src, name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		sum := sha256.Sum256(body)
		checksum := hex.EncodeToString(sum[:])

		if prev, ok := applied[name]; ok {
			if prev != checksum {
				return fmt.Errorf("migration %s was modified after it was applied "+
					"(recorded %s, found %s): add a new migration instead",
					name, prev[:12], checksum[:12])
			}
			continue
		}

		if err := applyOne(ctx, conn.Conn(), name, string(body), checksum); err != nil {
			return err
		}
		log.Info("migration applied", "version", name)
	}

	return nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, name, body, checksum string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Simple protocol: migration files contain multiple statements and
	// dollar-quoted function bodies, which the extended protocol rejects.
	if _, err := tx.Exec(ctx, body, pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("apply %s: %w", name, err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`,
		name, checksum); err != nil {
		return fmt.Errorf("record %s: %w", name, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", name, err)
	}
	return nil
}

func loadApplied(ctx context.Context, conn *pgx.Conn) (map[string]string, error) {
	rows, err := conn.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("load applied migrations: %w", err)
	}
	defer rows.Close()

	applied := map[string]string{}
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = checksum
	}
	return applied, rows.Err()
}

func sqlFiles(src fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
