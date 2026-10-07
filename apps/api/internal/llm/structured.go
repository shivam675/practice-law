package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Validator lets a destination type reject a response that parsed but does
// not make sense. A score above its maximum is well-formed JSON and still
// wrong, and catching it here means the repair retry gets a chance at it
// instead of the caller discovering it three layers up.
type Validator interface {
	Validate() error
}

// ErrUnstructured is returned when the model could not produce the shape
// after its one repair attempt. Callers degrade; they never retry in a loop.
// A judge decision that cannot be parsed becomes `continue`, which is why a
// rejected response must be distinguishable from a provider outage.
var ErrUnstructured = errors.New("model did not return the requested shape")

// StructuredResponse carries both attempts' token usage, because a repair
// costs real money and hiding it makes the ledger lie.
type StructuredResponse struct {
	ChatResponse
	Repaired bool
}

// Structured asks for JSON, parses it into dst, and on failure gives the model
// exactly one chance to fix its own output before giving up.
//
// One retry, not three. A model that cannot produce the shape twice will not
// produce it on the fifth attempt either, and a live session has a latency
// budget that a retry loop destroys.
func (c *Client) Structured(ctx context.Context, req ChatRequest, dst any) (StructuredResponse, error) {
	first, err := c.Chat(ctx, req)
	if err != nil {
		return StructuredResponse{ChatResponse: first}, err
	}

	parseErr := decodeInto(first.Content, dst)
	if parseErr == nil {
		return StructuredResponse{ChatResponse: first}, nil
	}

	repair := req
	repair.Messages = append(append([]Message{}, req.Messages...),
		Message{Role: "assistant", Content: first.Content},
		Message{Role: "user", Content: "That response could not be used: " +
			parseErr.Error() + "\n\nReply again with the JSON object only. " +
			"No prose, no code fence, no explanation."})

	second, err := c.Chat(ctx, repair)
	// Usage from both attempts; the ledger records what was actually spent.
	merged := second
	merged.InputTokens += first.InputTokens
	merged.OutputTokens += first.OutputTokens
	merged.Latency += first.Latency
	if err != nil {
		return StructuredResponse{ChatResponse: merged, Repaired: true}, err
	}

	if err := decodeInto(second.Content, dst); err != nil {
		return StructuredResponse{ChatResponse: merged, Repaired: true},
			fmt.Errorf("%w: %v", ErrUnstructured, err)
	}
	return StructuredResponse{ChatResponse: merged, Repaired: true}, nil
}

func decodeInto(content string, dst any) error {
	raw := extractJSON(content)
	if raw == "" {
		return errors.New("no JSON object found in the response")
	}

	// Permissive on purpose. An extra field the model volunteered is a style
	// disagreement, not a failure, and a strict pass that half-fills dst
	// before erroring leaves stale values behind for the next attempt.
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		return err
	}

	if v, ok := dst.(Validator); ok {
		if err := v.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// extractJSON finds the object in a response that may be wrapped in a code
// fence or trailed by an apology. Models do both, and refusing the whole call
// over a stray ```json is a retry nobody needs to pay for.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)

	// Qwen and friends emit a reasoning block before the answer.
	if i := strings.LastIndex(s, "</think>"); i >= 0 {
		s = strings.TrimSpace(s[i+len("</think>"):])
	}

	if fence := strings.Index(s, "```"); fence >= 0 {
		rest := s[fence+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			if lang := strings.TrimSpace(rest[:nl]); lang == "" || lang == "json" {
				rest = rest[nl+1:]
			}
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		s = strings.TrimSpace(rest)
	}

	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return ""
	}
	opener, closer := byte('{'), byte('}')
	if s[start] == '[' {
		opener, closer = '[', ']'
	}

	// Brace matching rather than LastIndex: prose after the object may well
	// contain a closing brace, and counting is the only way to find the end
	// of the first complete value.
	depth, inString, escaped := 0, false, false
	for i := start; i < len(s); i++ {
		ch := s[i]
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inString:
			escaped = true
		case ch == '"':
			inString = !inString
		case inString:
		case ch == opener:
			depth++
		case ch == closer:
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
