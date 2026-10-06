// Package aiclient talks to the AI plane.
//
// The AI plane is stateless and holds no authority: it is given bytes or text
// and returns a structured answer, which the control plane then validates.
package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		// Generous: a 300-page PDF takes seconds to parse. Document parsing
		// happens on upload, not in a live session, so this is not on any
		// latency-sensitive path.
		http: &http.Client{Timeout: 120 * time.Second},
	}
}

// Extraction is what the AI plane reports about a document.
//
// Pages is nil for formats that cannot report a page count without rendering,
// which includes DOCX. A nil here means "unknown", never "zero".
type Extraction struct {
	Format    string `json:"format"`
	Text      string `json:"text"`
	Pages     *int   `json:"pages"`
	Chars     int    `json:"chars"`
	Truncated bool   `json:"truncated"`
}

// ExtractError carries the AI plane's own status so the caller can map a
// rejected document to a 4xx rather than reporting an internal failure.
type ExtractError struct {
	Status int
	Detail string
}

func (e *ExtractError) Error() string {
	return fmt.Sprintf("ai plane rejected the document (%d): %s", e.Status, e.Detail)
}

// Unsupported reports whether the document itself was the problem, as opposed
// to the service.
func (e *ExtractError) Unsupported() bool {
	return e.Status == http.StatusUnsupportedMediaType ||
		e.Status == http.StatusUnprocessableEntity ||
		e.Status == http.StatusRequestEntityTooLarge
}

func (c *Client) Extract(ctx context.Context, filename string, data []byte) (Extraction, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return Extraction{}, fmt.Errorf("build extract request: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return Extraction{}, fmt.Errorf("write extract payload: %w", err)
	}
	if err := writer.Close(); err != nil {
		return Extraction{}, fmt.Errorf("close extract payload: %w", err)
	}

	endpoint, err := url.JoinPath(c.baseURL, "/v1/extract")
	if err != nil {
		return Extraction{}, fmt.Errorf("build extract url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return Extraction{}, fmt.Errorf("create extract request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return Extraction{}, fmt.Errorf("call ai plane: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Extraction{}, &ExtractError{
			Status: resp.StatusCode,
			Detail: decodeDetail(detail),
		}
	}

	var out Extraction
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&out); err != nil {
		return Extraction{}, fmt.Errorf("decode extract response: %w", err)
	}
	return out, nil
}

func (c *Client) Healthy(ctx context.Context) error {
	endpoint, err := url.JoinPath(c.baseURL, "/healthz")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ai plane health returned %d", resp.StatusCode)
	}
	return nil
}

// decodeDetail pulls FastAPI's {"detail": "..."} out of an error body, falling
// back to the raw text.
func decodeDetail(body []byte) string {
	var payload struct {
		Detail any `json:"detail"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Detail != nil {
		if s, ok := payload.Detail.(string); ok {
			return s
		}
		if raw, err := json.Marshal(payload.Detail); err == nil {
			return string(raw)
		}
	}
	if len(body) > 512 {
		body = body[:512]
	}
	return string(body)
}
