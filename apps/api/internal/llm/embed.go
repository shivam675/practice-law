package llm

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type EmbedRequest struct {
	Model   string
	Input   []string
	Timeout time.Duration
}

type EmbedResponse struct {
	// Vectors is index-aligned with EmbedRequest.Input.
	Vectors     [][]float32
	Model       string
	InputTokens int
	Latency     time.Duration
}

// Embed calls the provider's embeddings endpoint. bge-m3 and every other
// model behind an OpenAI-compatible server speak this shape.
//
// Dimensionality is the caller's problem: document_chunks.embedding is
// vector(1024), and a model that returns something else must be caught before
// the insert, not after.
func (c *Client) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	if len(req.Input) == 0 {
		return EmbedResponse{}, nil
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	var out struct {
		Model string `json:"model"`
		Data  []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}

	started := time.Now()
	err := c.call(ctx, http.MethodPost, "embeddings", map[string]any{
		"model": req.Model,
		"input": req.Input,
	}, &out)
	latency := time.Since(started)
	if err != nil {
		return EmbedResponse{Latency: latency}, err
	}
	if len(out.Data) != len(req.Input) {
		return EmbedResponse{Latency: latency}, &Error{Detail: fmt.Sprintf(
			"asked for %d embeddings and got %d", len(req.Input), len(out.Data))}
	}

	// Place by index rather than trusting order. Batching servers do reorder,
	// and a silently transposed pair of vectors is a bug nothing would catch
	// until retrieval quietly returned the wrong chunk.
	vectors := make([][]float32, len(req.Input))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vectors) {
			return EmbedResponse{Latency: latency}, &Error{Detail: fmt.Sprintf(
				"provider returned embedding index %d, out of range", d.Index)}
		}
		if vectors[d.Index] != nil {
			return EmbedResponse{Latency: latency}, &Error{Detail: fmt.Sprintf(
				"provider returned index %d twice", d.Index)}
		}
		vectors[d.Index] = d.Embedding
	}
	for i, v := range vectors {
		if len(v) == 0 {
			return EmbedResponse{Latency: latency}, &Error{Detail: fmt.Sprintf(
				"provider returned no embedding for input %d", i)}
		}
	}

	return EmbedResponse{
		Vectors:     vectors,
		Model:       out.Model,
		InputTokens: out.Usage.PromptTokens,
		Latency:     latency,
	}, nil
}

// Vector renders an embedding in pgvector's text input format, which is what
// a parameterised insert needs without pulling in a driver extension for one
// column type.
func Vector(v []float32) string {
	var b strings.Builder
	b.Grow(len(v)*12 + 2)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
