package orgs

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/intelimek/megamoot/apps/api/internal/audit"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"
)

type Handlers struct {
	pool  *pgxpool.Pool
	audit *audit.Logger
	log   *slog.Logger
}

func NewHandlers(pool *pgxpool.Pool, auditLog *audit.Logger, log *slog.Logger) *Handlers {
	return &Handlers{pool, auditLog, log}
}

type Organization struct {
	ID                    uuid.UUID       `json:"id"`
	Name                  string          `json:"name"`
	Slug                  string          `json:"slug"`
	Status                string          `json:"status"`
	Settings              json.RawMessage `json:"settings"`
	MaxConcurrentSessions int             `json:"max_concurrent_sessions"`
}

const columns = `id,name,slug,status,settings,max_concurrent_sessions`

func scan(row pgx.Row) (Organization, error) {
	var o Organization
	err := row.Scan(&o.ID, &o.Name, &o.Slug, &o.Status, &o.Settings, &o.MaxConcurrentSessions)
	return o, err
}
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	if !p.Can("organization.edit") {
		httpx.Fail(w, r, httpx.ErrForbidden())
		return
	}
	o, err := scan(h.pool.QueryRow(r.Context(), `SELECT `+columns+` FROM organizations WHERE id=$1`, p.OrganizationID))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, o)
}

type settingsInput struct {
	Name     string `json:"name"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

var localePattern = regexp.MustCompile(`^[a-zA-Z]{2,8}(-[a-zA-Z0-9]{1,8})*$`)

func (in settingsInput) validate() error {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 {
		return httpx.ErrBadRequest("Organisation name must be 1 to 200 characters.")
	}
	if len(in.Locale) > 35 || !localePattern.MatchString(in.Locale) {
		return httpx.ErrBadRequest("Enter a language tag such as en or en-IN.")
	}
	if in.Timezone == "" || len(in.Timezone) > 100 {
		return httpx.ErrBadRequest("Choose a valid time zone.")
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return httpx.ErrBadRequest("Choose a valid time zone.")
	}
	return nil
}
func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	if !p.Can("organization.edit") {
		httpx.Fail(w, r, httpx.ErrForbidden())
		return
	}
	var in settingsInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := in.validate(); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	patch, err := json.Marshal(map[string]string{"locale": in.Locale, "timezone": in.Timezone})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	o, err := scan(h.pool.QueryRow(r.Context(), `UPDATE organizations SET name=$2,settings=settings||$3::jsonb WHERE id=$1 RETURNING `+columns, p.OrganizationID, in.Name, patch))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	h.audit.Record(r.Context(), audit.Entry{OrganizationID: &p.OrganizationID, ActorUserID: &p.UserID, Action: "organization.edit", TargetKind: "organization", TargetID: &p.OrganizationID, After: in, RequestID: httpx.RequestIDFrom(r.Context())})
	httpx.JSON(w, r, http.StatusOK, o)
}
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	if !p.Can("platform.organization.manage") {
		httpx.Fail(w, r, httpx.ErrForbidden())
		return
	}
	// This is a platform catalogue, deliberately spanning tenants behind the platform permission.
	rows, err := h.pool.Query(r.Context(), `SELECT `+columns+` FROM organizations ORDER BY name,id`)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	defer rows.Close()
	out := []Organization{}
	for rows.Next() {
		o, err := scan(rows)
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
		out = append(out, o)
	}
	if err = rows.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"organizations": out})
}
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	if !p.Can("platform.organization.manage") {
		httpx.Fail(w, r, httpx.ErrForbidden())
		return
	}
	var in struct {
		Name          string `json:"name"`
		AdminEmail    string `json:"admin_email"`
		AdminPassword string `json:"admin_password"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.AdminEmail = strings.TrimSpace(in.AdminEmail)
	if in.Name == "" || len(in.Name) > 200 {
		httpx.Fail(w, r, httpx.ErrBadRequest("Organisation name must be 1 to 200 characters."))
		return
	}
	address, err := mail.ParseAddress(in.AdminEmail)
	if err != nil || address.Address != in.AdminEmail {
		httpx.Fail(w, r, httpx.ErrBadRequest("A valid administrator email is required."))
		return
	}
	if err = auth.ValidateNewPassword(in.AdminPassword); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	id, err := Bootstrap(r.Context(), h.pool, BootstrapInput{RequireNew: true, OrgName: in.Name, AdminEmail: in.AdminEmail, AdminPassword: in.AdminPassword}, h.log)
	if err != nil {
		var state interface{ SQLState() string }
		if errors.As(err, &state) && state.SQLState() == "23505" {
			err = httpx.ErrConflict("An organisation with this address already exists.")
		}
		httpx.Fail(w, r, fmt.Errorf("create organization: %w", err))
		return
	}
	h.audit.Record(r.Context(), audit.Entry{OrganizationID: &id, ActorUserID: &p.UserID, Action: "platform.organization.create", TargetKind: "organization", TargetID: &id, After: map[string]any{"name": in.Name, "slug": Slugify(in.Name)}, RequestID: httpx.RequestIDFrom(r.Context())})
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"id": id, "slug": Slugify(in.Name)})
}
