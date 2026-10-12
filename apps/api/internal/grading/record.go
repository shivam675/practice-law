package grading

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/slmlabs/megamoot/apps/api/internal/rubrics"
)

// recordBudget is how much of the record one criterion call reads. A small
// model loses the rubric somewhere past a few thousand tokens of evidence, so
// a long record is cut down to the passages that bear on the criterion
// rather than read in several summarising passes.
const recordBudget = 9000

// chunkChars is the size of one candidate passage, and outlineChars bounds
// the list of headings that shows a criterion like structure the document's
// shape when its passages cannot all fit.
const (
	chunkChars   = 1200
	outlineChars = 1500
)

// excerpt returns the record whole when it fits, and otherwise the opening
// passage of each source stage plus the passages sharing most words with the
// criterion, in their original order. Everything returned is a verbatim slice
// of the record, so evidence quotes still verify against the full text.
//
// ponytail: word overlap, not embeddings. Swap in retrieval over indexed
// submission chunks when a criterion's vocabulary stops matching the record's.
func excerpt(text string, c rubrics.Criterion) string {
	if len(text) <= recordBudget {
		return text
	}
	chunks := chunkRecord(text)
	outline := headings(text)
	terms := ContentWords(c.Name + " " + c.Description + " " + c.Guidance)
	type ranked struct {
		index int
		score int
	}
	order := make([]ranked, 0, len(chunks))
	keep := map[int]bool{}
	used := len(outline)
	for i, ch := range chunks {
		if ch.first && used+len(ch.text) <= recordBudget {
			keep[i] = true
			used += len(ch.text)
			continue
		}
		hits := 0
		for w := range ContentWords(ch.text) {
			if terms[w] {
				hits++
			}
		}
		order = append(order, ranked{i, hits})
	}
	sort.SliceStable(order, func(a, b int) bool { return order[a].score > order[b].score })
	for _, r := range order {
		if used+len(chunks[r.index].text) > recordBudget {
			continue
		}
		keep[r.index] = true
		used += len(chunks[r.index].text)
	}
	var b strings.Builder
	b.WriteString("Excerpts of the record selected for this criterion. Omitted passages are marked [...].\n\n")
	if outline != "" {
		b.WriteString("Headings in the full record:\n" + outline + "\n\n")
	}
	gap := false
	for i, ch := range chunks {
		if !keep[i] {
			gap = true
			continue
		}
		if gap {
			b.WriteString("[...]\n\n")
			gap = false
		}
		if !ch.first && ch.stage != "" {
			b.WriteString("[" + ch.stage + "]\n")
		}
		b.WriteString(ch.text)
		b.WriteString("\n\n")
	}
	if gap {
		b.WriteString("[...]\n")
	}
	return strings.TrimSpace(b.String())
}

// headings lists the lines that look like headings: short, not ending as a
// sentence does. A short transcript turn can slip in; it is still verbatim.
//
// ponytail: a line-shape guess. Use the parser's heading markup once document
// extraction keeps it.
func headings(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 3 || len(line) > 90 || strings.ContainsAny(line[len(line)-1:], ".,;\"'") || !strings.ContainsFunc(line, unicode.IsLetter) {
			continue
		}
		if b.Len()+len(line)+1 > outlineChars {
			break
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimSpace(b.String())
}

type chunk struct {
	text  string
	stage string // the [stage] heading the passage sits under
	first bool   // the opening passage of its stage
}

// chunkRecord splits on lines, so transcript turns and paragraphs stay whole,
// and starts a new chunk at every [stage] heading that loadSubmission writes.
func chunkRecord(text string) []chunk {
	var out []chunk
	var b strings.Builder
	stage, first := "", false
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, chunk{text: s, stage: stage, first: first})
			first = false
		}
		b.Reset()
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 2 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']' && !strings.ContainsAny(trimmed, " \t") {
			flush()
			stage, first = trimmed[1:len(trimmed)-1], true
			b.WriteString(trimmed + "\n")
			continue
		}
		for len(line) > chunkChars {
			// One paragraph longer than a chunk: cut at a space.
			cut := strings.LastIndexByte(line[:chunkChars], ' ')
			if cut <= 0 {
				cut = chunkChars
				for !utf8.RuneStart(line[cut]) {
					cut--
				}
			}
			flush()
			b.WriteString(line[:cut])
			flush()
			line = strings.TrimLeft(line[cut:], " ")
		}
		if b.Len()+len(line) > chunkChars {
			flush()
		}
		b.WriteString(line + "\n")
	}
	flush()
	return out
}

var stopWords = map[string]bool{
	"about": true, "above": true, "after": true, "again": true, "also": true, "been": true,
	"being": true, "both": true, "could": true, "does": true, "each": true, "from": true,
	"have": true, "into": true, "more": true, "most": true, "must": true, "only": true,
	"other": true, "over": true, "same": true, "should": true, "some": true, "such": true,
	"than": true, "that": true, "their": true, "them": true, "then": true, "there": true,
	"these": true, "they": true, "this": true, "those": true, "very": true, "were": true,
	"what": true, "when": true, "where": true, "which": true, "while": true, "with": true,
	"would": true, "your": true, "will": true, "counsel": true, "court": true,
	"whether": true, "because": true, "please": true,
}

// ContentWords lowercases, drops short and common words, and trims a plural
// or -ing ending so "authorities" meets "authority" halfway.
func ContentWords(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) < 4 || stopWords[w] {
			continue
		}
		for _, suffix := range []string{"ies", "ing", "es", "s"} {
			if strings.HasSuffix(w, suffix) && len(w)-len(suffix) >= 4 {
				w = strings.TrimSuffix(w, suffix)
				break
			}
		}
		out[w] = true
	}
	return out
}
