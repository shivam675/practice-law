package platformcfg

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/intelimek/megamoot/apps/api/internal/audit"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/intelimek/megamoot/apps/api/internal/llm"
)

type Handlers struct {
	store  *Store
	ledger *llm.Ledger
	audit  *audit.Logger
}

func NewHandlers(store *Store, ledger *llm.Ledger, auditLog *audit.Logger) *Handlers {
	return &Handlers{store: store, ledger: ledger, audit: auditLog}
}

func (h *Handlers) ListProviders(w http.ResponseWriter, r *http.Request) {
	out, err := h.store.ListProviders(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"providers": out, "tiers": Tiers})
}

func (h *Handlers) CreateProvider(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	var in ProviderInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out, err := h.store.CreateProvider(r.Context(), p.UserID, in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// The key never reaches the audit log either. Whether one was set is the
	// useful fact; what it was is not.
	h.record(r, p, "platform.provider.create", out.ID, map[string]any{
		"key": out.Key, "base_url": out.BaseURL, "has_api_key": out.HasAPIKey,
	})
	httpx.JSON(w, r, http.StatusCreated, out)
}

func (h *Handlers) UpdateProvider(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "providerID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid provider id."))
		return
	}

	var in ProviderPatch
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out, err := h.store.UpdateProvider(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.record(r, p, "platform.provider.edit", out.ID, map[string]any{
		"base_url": out.BaseURL, "is_active": out.IsActive,
		"api_key_replaced": in.APIKey != nil,
	})
	httpx.JSON(w, r, http.StatusOK, out)
}

func (h *Handlers) DeleteProvider(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "providerID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid provider id."))
		return
	}
	if err := h.store.DeleteProvider(r.Context(), id); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.record(r, p, "platform.provider.delete", id, nil)
	httpx.JSON(w, r, http.StatusNoContent, nil)
}

func (h *Handlers) ListBindings(w http.ResponseWriter, r *http.Request) {
	out, err := h.store.ListBindings(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"bindings": out, "tiers": Tiers})
}

func (h *Handlers) PutBindings(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	var in struct {
		Bindings []BindingInput `json:"bindings"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out, err := h.store.PutBindings(r.Context(), p.UserID, in.Bindings)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	routing := map[string]any{}
	for _, b := range out {
		routing[b.Tier] = b.ProviderKey + " / " + b.Model
	}
	h.record(r, p, "platform.binding.set", uuid.Nil, routing)
	httpx.JSON(w, r, http.StatusOK, map[string]any{"bindings": out, "tiers": Tiers})
}

/* --------------------------------------------------------------------- test */

type TestInput struct {
	// Either a tier, which exercises the binding and its parameters, or a
	// provider on its own, which only proves the endpoint answers.
	Tier       string     `json:"tier"`
	ProviderID *uuid.UUID `json:"provider_id"`
	Prompt     string     `json:"prompt"`
}

type TestResult struct {
	OK           bool     `json:"ok"`
	Reachable    bool     `json:"reachable"`
	Models       []string `json:"models"`
	Model        string   `json:"model"`
	Sample       string   `json:"sample"`
	LatencyMS    int64    `json:"latency_ms"`
	Error        string   `json:"error"`
	Unauthorized bool     `json:"unauthorized"`
}

// Test calls the provider for real. Nothing about a stored configuration is
// trustworthy until something has dialled it, and an operator finding out
// during a graded session is the wrong time.
func (h *Handlers) Test(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	var in TestInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if in.Tier == "" && in.ProviderID == nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Give either a tier or a provider_id."))
		return
	}

	var (
		provider llm.Provider
		binding  Binding
		err      error
	)
	if in.Tier != "" {
		provider, binding, err = h.store.Resolve(r.Context(), in.Tier)
	} else {
		provider, err = h.store.credentials(r.Context(), *in.ProviderID)
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	client := llm.New(provider)
	result := TestResult{}

	// Listing models is the cheap half: it proves the host, the TLS chain and
	// the bearer token without occupying the GPU.
	listCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	models, err := client.Models(listCtx)
	if err != nil {
		result.Error = err.Error()
		result.Unauthorized = unauthorized(err)
		httpx.JSON(w, r, http.StatusOK, result)
		return
	}
	result.Reachable = true
	result.Models = models

	if binding.Model == "" {
		// Provider-only test. The endpoint answers; nothing more is claimed.
		result.OK = true
		httpx.JSON(w, r, http.StatusOK, result)
		return
	}

	prompt := in.Prompt
	if prompt == "" {
		prompt = "Reply with the single word: ready."
	}

	chat, callErr := client.Chat(r.Context(), llm.ChatRequest{
		Model:       binding.Model,
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		Temperature: binding.Temperature,
		TopP:        binding.TopP,
		MaxTokens:   min(binding.MaxTokens, 64),
		Timeout:     time.Duration(binding.TimeoutMS) * time.Millisecond,
	})

	orgID := p.OrganizationID
	h.ledger.Record(r.Context(), llm.Entry{
		OrganizationID: &orgID,
		TraceID:        httpx.RequestIDFrom(r.Context()),
		Purpose:        "connection_test",
		Provider:       binding.ProviderKey,
		Model:          binding.Model,
		ModelTier:      binding.Tier,
		InputTokens:    chat.InputTokens,
		OutputTokens:   chat.OutputTokens,
		Latency:        chat.Latency,
		Err:            callErr,
	})

	result.Model = binding.Model
	result.LatencyMS = chat.Latency.Milliseconds()
	if callErr != nil {
		result.Error = callErr.Error()
		result.Unauthorized = unauthorized(callErr)
		httpx.JSON(w, r, http.StatusOK, result)
		return
	}

	result.OK = true
	result.Sample = truncate(chat.Content, 500)
	httpx.JSON(w, r, http.StatusOK, result)
}

/* -------------------------------------------------------------------- utils */

func (h *Handlers) record(r *http.Request, p auth.Principal, action string, target uuid.UUID, after any) {
	orgID := p.OrganizationID
	entry := audit.Entry{
		OrganizationID: &orgID,
		ActorUserID:    &p.UserID,
		Action:         action,
		TargetKind:     "model_provider",
		After:          after,
		RequestID:      httpx.RequestIDFrom(r.Context()),
	}
	if target != uuid.Nil {
		entry.TargetID = &target
	}
	h.audit.Record(r.Context(), entry)
}

func unauthorized(err error) bool {
	var provErr *llm.Error
	return errors.As(err, &provErr) && provErr.Unauthorized()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
