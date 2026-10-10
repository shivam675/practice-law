package grading

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRecordPartsPreserveEveryCharacter(t *testing.T) {
	text := strings.Repeat("न्याय evidence\n\n", 9000) + "critical closing argument"
	parts := recordParts(text, maxInlineChars)
	if strings.Join(parts, "") != text || len(parts) < 2 {
		t.Fatal("record was truncated")
	}
	for _, part := range parts {
		if len(part) > maxInlineChars || !utf8.ValidString(part) {
			t.Fatal("invalid chunk boundary")
		}
	}
}
