# Administration implementation report

## Delivered

- Team name and membership editing reuses the create form. Existing assigned teams expose `membership_locked`, allow name changes, and refuse membership/role/speaking-order changes. Team edits hold a row lock; assignment membership validation (`teams.Size`) takes a share lock so a concurrent assignment cannot bypass the history check.
- Organisation name, locale and timezone settings use `organization.edit`; edits merge only those setting keys, preserving other stored settings and the platform session ceiling. Platform catalogue/creation requires `platform.organization.manage`; creation reuses `orgs.Bootstrap` with a `RequireNew` guard and slug advisory lock so conflicting creation cannot add an administrator to an existing tenant.
- Administrator-assisted password recovery requires `user.edit`, same-tenant target resolution, and `platform.organization.manage` for a target with platform roles. Link issuance revokes prior links. Only a SHA-256 digest of a random 32-byte token is stored; lifetime is 15 minutes. Links use URL fragments, are removed from the browser URL once opened, and are never logged or audited as plaintext.
- Reset atomically consumes the token, changes the Argon2 password hash, clears login lockout, and revokes all refresh sessions. It rechecks issuer authorization and platform-target status. Access tokens also stop working through the existing family revocation check. Session creation now locks the user and checks the expected password hash, preventing a concurrent old-password login from creating a session after reset. Rehash writes are conditional to prevent overwriting a concurrent reset.
- People page displays a temporary private reset link. The reset page works without authentication once wired. No email delivery or external provider.

## Owned files

- API: `internal/teams/edit.go`, `edit_test.go`, `teams.go`; `internal/orgs/handlers.go`, `handlers_test.go`, `seed.go`; `internal/auth/recovery.go`, `recovery_test.go`, `recovery_integration_test.go`, `store.go`, `service.go`.
- Migration: `apps/api/migrations/0010_admin_recovery.sql`.
- Web: `routes/Teams.tsx`, `People.tsx`, `PasswordRecovery.tsx`, `ResetPassword.tsx`, `Organizations.tsx`, and the Team type in `lib/api.ts`.
- Shared router, App and AppShell were not edited.

## Exact shared wiring

In `apps/api/cmd/api/router.go`, import `internal/orgs` and instantiate:

```go
organizationHandlers := orgs.NewHandlers(a.pool, a.audit, a.log)
```

Inside `/auth`, alongside the existing rate-limited auth group:

```go
r.With(loginLimiter.LimitByIP).Post("/reset-password", authHandlers.ResetPassword)
```

Inside authenticated `/users`:

```go
r.With(authz.Require("user.edit")).Post("/{userID}/password-reset", authHandlers.IssuePasswordReset)
```

Inside authenticated `/teams`:

```go
r.With(authz.Require("team.edit")).Put("/{teamID}", teamHandlers.Update)
```

Inside the authenticated API group:

```go
r.With(authz.Require("organization.edit")).Get("/organization", organizationHandlers.Get)
r.With(authz.Require("organization.edit")).Put("/organization", organizationHandlers.Update)
r.With(authz.Require("platform.organization.manage")).Get("/admin/organizations", organizationHandlers.List)
r.With(authz.Require("platform.organization.manage")).Post("/admin/organizations", organizationHandlers.Create)
```

In `apps/web/src/App.tsx`:

```tsx
import { OrganizationSettings, Organizations } from "./routes/Organizations";
import { ResetPassword } from "./routes/ResetPassword";
```

Add `<Route path="/reset-password" element={<ResetPassword />} />` both to the signed-out Routes before its wildcard SignIn route, and to the signed-in Routes outside AppShell. Add inside AppShell:

```tsx
<Route path="/admin/organization" element={<OrganizationSettings />} />
<Route path="/admin/organizations" element={<Organizations />} />
```

In AppShell navigation, reuse an existing icon such as Stack:

```tsx
{ to: "/admin/organization", label: "Organisation", icon: Stack, permission: "organization.edit" },
{ to: "/admin/organizations", label: "Organisations", icon: Stack, permission: "platform.organization.manage" },
```

## Verification

- `docker compose exec -T api go test ./...`: passes.
- `docker compose exec -T web npm run build`: passes.
- `ADMIN_TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/auth -run TestRecoveryDatabase -v` in the API container: passes. Creates only a disposable tenant, users, roles and tokens, then deletes those records. Checks tenant and platform protection, expiry, superseded tokens, concurrent double consumption, password storage, session revocation, replay, and rejection of stale password verification during new-session creation.
- Helper tests cover membership identity/order changes, Unicode password length, token encoding/hash, privilege checks, and organisation locale/timezone validation.
- Migration was available during the isolated DB test through the existing development reload. No real user's password was changed and no records were committed to Git.

## Limits

Browser interaction and HTTP route smoke remain for the coordinator after shared wiring. Organisation regional preferences are stored metadata; this change does not retrofit every existing date formatter or localize interface text. The existing directory form loads up to 200 users, but edit mode preserves any existing selected members outside that page. No email provider, bulk administration, or platform-wide tenant impersonation was introduced.
