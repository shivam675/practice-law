package auth

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sync"
	"testing"
	"time"
)

// Runs only when explicitly requested; all records belong to a disposable test tenant.
func TestRecoveryDatabase(t *testing.T) {
	url := os.Getenv("ADMIN_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ADMIN_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	orgID, issuerID, targetID, roleID, platformRole := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO organizations(id,slug,name) VALUES($1,$2,'Recovery test')`, orgID, "recovery-test-"+orgID.String())
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID); err != nil {
			t.Error(err)
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM roles WHERE id=$1 AND organization_id IS NULL`, platformRole); err != nil {
			t.Error(err)
		}
	}()
	oldPassword, newPassword := "Recovery-test-old-123", "Recovery-test-new-456"
	hash, err := HashPassword(oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO users(id,organization_id,email,email_normalized,full_name,password_hash) VALUES($1,$3,'issuer@test.invalid','issuer@test.invalid','Issuer',$4),($2,$3,'target@test.invalid','target@test.invalid','Target',$4)`, issuerID, targetID, orgID, hash)
	exec(`INSERT INTO roles(id,organization_id,key,name) VALUES($1,$2,'recovery_test_admin','Recovery administrator')`, roleID, orgID)
	exec(`INSERT INTO role_permissions(role_id,permission_key) VALUES($1,'user.edit')`, roleID)
	exec(`INSERT INTO user_roles(user_id,role_id) VALUES($1,$2)`, issuerID, roleID)
	svc := NewService(NewStore(pool), nil, nil)
	p := Principal{UserID: issuerID, OrganizationID: orgID, permissions: map[string]struct{}{"user.edit": {}}}
	cross := p
	cross.OrganizationID = uuid.New()
	if _, _, err := svc.issuePasswordReset(ctx, cross, targetID); err == nil {
		t.Fatal("cross-tenant reset issued")
	}
	exec(`INSERT INTO roles(id,key,name) VALUES($1,$2,'Recovery platform test')`, platformRole, "recovery_platform_"+platformRole.String())
	exec(`INSERT INTO user_roles(user_id,role_id) VALUES($1,$2)`, targetID, platformRole)
	if _, _, err := svc.issuePasswordReset(ctx, p, targetID); err == nil {
		t.Fatal("tenant administrator reset platform account")
	}
	exec(`DELETE FROM user_roles WHERE user_id=$1 AND role_id=$2`, targetID, platformRole)
	first, expires, err := svc.issuePasswordReset(ctx, p, targetID)
	if err != nil {
		t.Fatal(err)
	}
	if until := time.Until(expires); until < 14*time.Minute || until > 16*time.Minute {
		t.Fatal("wrong reset lifetime")
	}
	exec(`UPDATE password_reset_tokens SET expires_at=now()-interval '1 second' WHERE token_hash=$1 AND organization_id=$2`, HashRefreshToken(first), orgID)
	if _, _, err = svc.resetPassword(ctx, first, newPassword); err == nil {
		t.Fatal("expired token accepted")
	}
	first, _, err = svc.issuePasswordReset(ctx, p, targetID)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := svc.issuePasswordReset(ctx, p, targetID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.resetPassword(ctx, first, newPassword); err == nil {
		t.Fatal("superseded token accepted")
	}
	family := uuid.New()
	_, refreshHash, err := NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	rec := RefreshRecord{ID: uuid.New(), UserID: targetID, OrganizationID: orgID, FamilyID: family, ExpiresAt: time.Now().Add(time.Hour)}
	if err = svc.store.InsertRefreshToken(ctx, rec, refreshHash, nil, "test", nil, hash); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() { defer wait.Done(); _, _, err := svc.resetPassword(ctx, token, newPassword); results <- err }()
	}
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("one token succeeded %d times", successes)
	}
	var current string
	var revoked bool
	if err = pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1 AND organization_id=$2`, targetID, orgID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyPassword(current, newPassword); err != nil {
		t.Fatal("new password not stored")
	}
	if revoked, err = svc.store.FamilyRevoked(ctx, family); err != nil || !revoked {
		t.Fatal("session survived reset")
	}
	rec.ID = uuid.New()
	rec.FamilyID = uuid.New()
	if err = svc.store.InsertRefreshToken(ctx, rec, refreshHash, nil, "stale login", nil, hash); err == nil {
		t.Fatal("stale password verification created a new session")
	}
	if _, _, err = svc.resetPassword(ctx, token, oldPassword); err == nil {
		t.Fatal("used token replayed")
	}
}
