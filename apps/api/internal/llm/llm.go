// Package llm is the provider adapter and the request ledger.
//
// One shape goes out, whatever the provider: the application never writes a
// provider-specific request. Today that shape is OpenAI's chat completions,
// which is what Ollama, vLLM, llama.cpp, LM Studio and the hosted providers
// all speak. Swapping provider is a row in model_providers, not a deploy.
//
// Nothing here decides anything. It calls, it times, it returns. Validation,
// bounds checking and persistence belong to the caller.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Provider struct {
	Kind    string
	BaseURL string
	APIKey  string
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string
	Messages    []Message
	Temperature float64
	TopP        float64
	MaxTokens   int
	Timeout     time.Duration

	// ReasoningEffort is the OpenAI-compatible thinking budget: none, low,
	// medium or high. Empty sends nothing and the provider keeps its default.
	// It matters because a reasoning model spends MaxTokens on its thinking
	// before it writes any answer at all.
	ReasoningEffort string
}

type ChatResponse struct {
	Content      string
	Model        string
	InputTokens  int
	OutputTokens int
	Latency      time.Duration
}

// Error carries the provider's own status so a caller can tell a bad
// credential from a cold model from a box that is simply not there.
type Error struct {
	Status  int
	Detail  string
	Timeout bool
}

func (e *Error) Error() string {
	switch {
	case e.Timeout:
		return "provider timed out: " + e.Detail
	case e.Status == 0:
		return "provider unreachable: " + e.Detail
	}
	return fmt.Sprintf("provider returned %d: %s", e.Status, e.Detail)
}

// Unauthorized separates a wrong bearer token from everything else, because
// that is the one failure an operator can fix from the settings page.
func (e *Error) Unauthorized() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

type Client struct {
	provider Provider
	http     *http.Client
}

func New(p Provider) *Client {
	return &Client{
		provider: p,
		// Per-request deadlines come from the binding's timeout_ms. This is
		// the backstop for a provider that accepts a connection and then
		// never speaks.
		http: &http.Client{Timeout: 10 * time.Minute},
	}
}

// Models lists what the provider will serve, which is how the settings page
// offers a model menu instead of a free-text box an operator can typo.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.call(ctx, http.MethodGet, "models", nil, &out); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			names = append(names, m.ID)
		}
	}
	return names, nil
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	body := map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   false,
	}
	if req.Temperature >= 0 {
		body["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.ReasoningEffort != "" {
		body["reasoning_effort"] = req.ReasoningEffort
	}

	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	started := time.Now()
	err := c.call(ctx, http.MethodPost, "chat/completions", body, &out)
	latency := time.Since(started)
	if err != nil {
		return ChatResponse{Latency: latency}, err
	}
	if len(out.Choices) == 0 {
		return ChatResponse{Latency: latency}, &Error{Detail: "provider returned no choices"}
	}

	// A reasoning model spends max_tokens on its thinking before it writes a
	// single character of the answer. Truncated there, the content is empty and
	// every parser downstream reports a missing JSON object instead of the real
	// cause, so name it here.
	if strings.TrimSpace(out.Choices[0].Message.Content) == "" && out.Choices[0].FinishReason == "length" {
		return ChatResponse{Latency: latency, OutputTokens: out.Usage.CompletionTokens},
			&Error{Detail: fmt.Sprintf("response hit max_tokens (%d) before the answer began; a reasoning model needs a larger max_tokens", req.MaxTokens)}
	}

	return ChatResponse{
		Content:      out.Choices[0].Message.Content,
		Model:        out.Model,
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
		Latency:      latency,
	}, nil
}

func (c *Client) call(ctx context.Context, method, path string, body any, dst any) error {
	endpoint, err := url.JoinPath(normalizeBase(c.provider.BaseURL), path)
	if err != nil {
		return fmt.Errorf("build %s url: %w", path, err)
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", path, err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("create %s request: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.provider.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.provider.APIKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
			return &Error{Timeout: true, Detail: "request timed out"}
		}
		return &Error{Detail: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &Error{Status: resp.StatusCode, Detail: summarise(detail)}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(dst); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}

// normalizeBase accepts the endpoint with or without its /v1 suffix, because
// an operator pasting a URL from a provider's documentation gets it either
// way and neither should be wrong.
func normalizeBase(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

// summarise pulls the message out of an OpenAI-shaped error body. The raw
// body is the fallback: a reverse proxy in front of the model will return
// HTML and an operator needs to see that it did.
func summarise(body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error.Message != "" {
		return payload.Error.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	if s == "" {
		return "empty response body"
	}
	return s
}
