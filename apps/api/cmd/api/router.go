package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/intelimek/megamoot/apps/api/internal/aiprofiles"
	"github.com/intelimek/megamoot/apps/api/internal/assessments"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/authz"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/intelimek/megamoot/apps/api/internal/orgs"
	"github.com/intelimek/megamoot/apps/api/internal/platformcfg"
	"github.com/intelimek/megamoot/apps/api/internal/reports"
	"github.com/intelimek/megamoot/apps/api/internal/rubrics"
	"github.com/intelimek/megamoot/apps/api/internal/sessions"
	"github.com/intelimek/megamoot/apps/api/internal/submissions"
	"github.com/intelimek/megamoot/apps/api/internal/teams"
	"github.com/intelimek/megamoot/apps/api/internal/templates"
	"github.com/intelimek/megamoot/apps/api/internal/users"
)

func newRouter(a *app) http.Handler {
	r := chi.NewRouter()

	r.Use(httpx.RequestID)
	r.Use(httpx.WithLogger(a.log))
	r.Use(httpx.Recover)
	r.Use(httpx.AccessLog)
	r.Use(httpx.SecurityHeaders)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   a.cfg.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-Id"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	live, ready := healthHandlers(a.pool)
	r.Get("/healthz", live)
	r.Get("/readyz", ready)

	authHandlers := auth.NewHandlers(a.auth, a.cfg.CookieDomain, a.cfg.CookieSecure)
	userHandlers := users.NewHandlers(a.pool, a.audit)
	orgHandlers := orgs.NewHandlers(a.pool, a.audit, a.log)
	rubricHandlers := rubrics.NewHandlers(a.rubrics, a.audit)
	templateHandlers := templates.NewHandlers(a.templates, a.audit)
	teamHandlers := teams.NewHandlers(a.teams, a.audit)
	assessmentHandlers := assessments.NewHandlers(a.assessments, a.audit)
	submissionHandlers := submissions.NewHandlers(a.submissions, a.audit)
	platformHandlers := platformcfg.NewHandlers(a.platform, a.ledger, a.audit)
	profileHandlers := aiprofiles.NewHandlers(a.aiProfiles, a.audit)
	reportHandlers := reports.New(a.pool, a.engine, a.grading)
	sessionHandlers := sessions.New(a.pool, a.engine, a.harness, a.cfg.MediaServiceToken, a.cfg.MaxConcurrentLiveSessions)

	// Credential endpoints get their own bucket. Everything else shares a
	// looser one; per-tenant quotas arrive with usage accounting.
	loginLimiter := httpx.NewRateLimiter(20, 5)
	apiLimiter := httpx.NewRateLimiter(600, 120)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(apiLimiter.LimitByIP)
		r.Get("/media/session", sessionHandlers.Verify)
		r.Post("/media/turn", sessionHandlers.Turn)
		r.Post("/media/finish", sessionHandlers.Finish)
		r.Post("/media/question", sessionHandlers.Question)

		r.Route("/auth", func(r chi.Router) {
			r.With(loginLimiter.LimitByIP).Post("/reset-password", authHandlers.ResetPassword)
			r.With(loginLimiter.LimitByIP).Group(authHandlers.Routes)
		})

		r.Group(func(r chi.Router) {
			r.Use(a.auth.Authenticate)

			r.Get("/me", authHandlers.Me)
			r.With(authz.Require("organization.edit")).Get("/organization", orgHandlers.Get)
			r.With(authz.Require("organization.edit")).Put("/organization", orgHandlers.Update)
			r.With(authz.Require("platform.organization.manage")).Get("/admin/organizations", orgHandlers.List)
			r.With(authz.Require("platform.organization.manage")).Post("/admin/organizations", orgHandlers.Create)
			r.With(authz.RequireAny("session.join", "session.observe")).Get("/sessions/{sessionID}", sessionHandlers.Get)
			r.With(authz.RequireAny("session.join", "session.moderate")).Post("/sessions/{sessionID}/end", sessionHandlers.End)

			r.Route("/users", func(r chi.Router) {
				r.With(authz.Require("user.edit")).Post("/{userID}/password-reset", authHandlers.IssuePasswordReset)
				r.With(authz.RequireAny("user.create", "user.assign_role")).Get("/roles", userHandlers.Roles)
				r.With(authz.Require("user.view")).Get("/", userHandlers.List)
				r.With(authz.Require("user.create")).Post("/", userHandlers.Create)
				r.With(authz.Require("user.assign_role")).
					Put("/{userID}/roles", userHandlers.SetRoles)
			})

			r.Route("/rubrics", func(r chi.Router) {
				r.With(authz.Require("rubric.view")).Get("/", rubricHandlers.List)
				r.With(authz.Require("rubric.view")).Get("/{rubricID}", rubricHandlers.Get)
				r.With(authz.Require("rubric.create")).Post("/", rubricHandlers.Create)
			})

			r.Route("/templates", func(r chi.Router) {
				r.With(authz.Require("template.view")).Get("/", templateHandlers.List)
				r.With(authz.Require("template.create")).Post("/", templateHandlers.Create)
				r.With(authz.Require("template.edit")).
					Post("/{templateID}/versions", templateHandlers.CreateVersion)
				r.With(authz.Require("template.view")).
					Get("/versions/{versionID}", templateHandlers.GetVersion)
				r.With(authz.Require("template.edit")).
					Post("/versions/{versionID}/publish", templateHandlers.PublishVersion)
			})

			r.Route("/teams", func(r chi.Router) {
				r.With(authz.Require("team.edit")).Put("/{teamID}", teamHandlers.Update)
				r.With(authz.Require("team.view")).Get("/", teamHandlers.List)
				r.With(authz.Require("team.view")).Get("/{teamID}", teamHandlers.Get)
				r.With(authz.Require("team.create")).Post("/", teamHandlers.Create)
			})

			r.Route("/assessments", func(r chi.Router) {
				r.With(authz.Require("knowledge.view")).Get("/{assessmentID}/resources", submissionHandlers.ListResources)
				r.With(authz.Require("knowledge.upload")).Post("/{assessmentID}/resources", submissionHandlers.UploadResource)
				r.With(authz.Require("assessment.view")).Get("/", assessmentHandlers.List)
				r.With(authz.Require("assessment.view")).
					Get("/{assessmentID}", assessmentHandlers.Get)
				r.With(authz.Require("assessment.create")).Post("/", assessmentHandlers.Create)
				r.With(authz.Require("assessment.publish")).
					Post("/{assessmentID}/publish", assessmentHandlers.Publish)
				r.With(authz.Require("assessment.assign")).
					Post("/{assessmentID}/assignments", assessmentHandlers.Assign)
				r.With(authz.Require("assessment.view")).
					Get("/{assessmentID}/assignments", assessmentHandlers.ListAssignments)
			})

			// A student reaches their own work here. The handler narrows the
			// query to their own team when they lack the organisation-wide
			// permission, so view_own cannot be used to read someone else's.
			r.Route("/assignments", func(r chi.Router) {
				r.With(authz.Require("assessment.grade")).Post("/{assignmentID}/report/regrade", reportHandlers.Regrade)
				r.With(authz.RequireAny("report.view", "report.view_own")).Get("/{assignmentID}/report", reportHandlers.Get)
				r.With(authz.Require("assessment.override_grade")).Patch("/{assignmentID}/report/scores", reportHandlers.Override)
				r.With(authz.Require("report.publish")).Post("/{assignmentID}/report/publish", reportHandlers.Publish)
				r.With(authz.Require("session.join")).Post("/{assignmentID}/stages/{stageID}/session", sessionHandlers.Join)
				r.With(authz.RequireAny("assessment.view", "assessment.view_own")).
					Get("/", assessmentHandlers.ListAssignments)
				r.With(authz.RequireAny("assessment.view", "assessment.view_own")).
					Get("/{assignmentID}", assessmentHandlers.GetAssignment)

				// Uploading requires team membership as well as the
				// permission; the handler checks it.
				r.With(authz.Require("submission.upload")).
					Post("/{assignmentID}/stages/{stageID}/submissions", submissionHandlers.Upload)
				r.With(authz.RequireAny("submission.view", "submission.view_own")).
					Get("/{assignmentID}/submissions", submissionHandlers.List)
				r.With(authz.RequireAny("knowledge.view", "assessment.view_own")).
					Get("/{assignmentID}/resources", assessmentHandlers.Resources)
			})

			// AI actors. Organisation scoped: how firm a judge is belongs to
			// the institution, which model answers does not.
			r.Route("/ai-profiles", func(r chi.Router) {
				r.With(authz.Require("ai_profile.view")).Get("/", profileHandlers.List)
				r.With(authz.Require("ai_profile.create")).Post("/", profileHandlers.Create)
				// An edit supersedes rather than mutates, so it is a POST and
				// it answers 201 with the new version.
				r.With(authz.Require("ai_profile.edit")).
					Post("/{profileID}/versions", profileHandlers.Revise)
				r.With(authz.Require("ai_profile.edit")).
					Put("/{profileID}/active", profileHandlers.SetActive)
			})

			// Platform operator only. platform.model.configure is a platform
			// permission; no organisation role can hold it.
			r.Route("/platform", func(r chi.Router) {
				r.Use(authz.Require("platform.model.configure"))

				r.Get("/providers", platformHandlers.ListProviders)
				r.Post("/providers", platformHandlers.CreateProvider)
				r.Patch("/providers/{providerID}", platformHandlers.UpdateProvider)
				r.Delete("/providers/{providerID}", platformHandlers.DeleteProvider)

				r.Get("/bindings", platformHandlers.ListBindings)
				r.Put("/bindings", platformHandlers.PutBindings)

				// Dials the provider for real. Its own bucket, because it is
				// the one route here that costs GPU time.
				r.With(httpx.NewRateLimiter(30, 5).LimitByIP).
					Post("/test", platformHandlers.Test)
			})

			r.Route("/submissions", func(r chi.Router) {
				r.With(authz.RequireAny("submission.view", "submission.view_own")).
					Get("/{artifactID}/download", submissionHandlers.Download)
			})
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, r, httpx.ErrNotFound())
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, r, httpx.Err(http.StatusMethodNotAllowed,
			"method_not_allowed", "That method is not allowed here."))
	})

	return r
}
