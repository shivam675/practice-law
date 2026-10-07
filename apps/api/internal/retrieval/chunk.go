package retrieval

import (
	"strconv"
	"strings"
	"unicode"
)

// Chunk is one retrievable passage. Locator is what a student eventually
// reads underneath a quote, so it has to mean something to a person: a
// section heading beats a character offset every time.
type Chunk struct {
	Index      int
	Locator    string
	Content    string
	TokenCount int
}

const (
	// targetChars is a compromise. Smaller chunks retrieve more precisely and
	// give tighter evidence locators; larger ones keep an argument and its
	// authority together. Roughly 300 tokens.
	targetChars = 1200
	// overlapChars carries the tail of one chunk into the next so a sentence
	// split across the boundary is still findable from either side.
	overlapChars = 160
	// minChars stops a trailing fragment becoming its own chunk, which
	// otherwise ranks highly on short queries while saying nothing.
	minChars = 120
	// maxHeadingChars: a heading is short. A paragraph that happens to lack a
	// full stop is not one.
	maxHeadingChars = 90
)

// Split turns extracted document text into chunks, tracking the most recent
// heading so every chunk carries an anchor.
//
// Deliberately generic. The compliance checker knows what a memorial's
// sections are called, and teaching the chunker the same vocabulary would tie
// retrieval over statutes and judgments to one rule set.
func Split(text string) []Chunk {
	paragraphs := paragraphs(text)
	if len(paragraphs) == 0 {
		return nil
	}

	var (
		chunks  []Chunk
		buf     strings.Builder
		heading string
		anchor  string
		ordinal int
	)

	flush := func() {
		body := strings.TrimSpace(buf.String())
		buf.Reset()
		if len(body) < minChars && len(chunks) > 0 {
			// Too small to stand alone: hand it back to the previous chunk
			// rather than letting a stub compete in the rankings.
			prev := &chunks[len(chunks)-1]
			prev.Content += "\n\n" + body
			prev.TokenCount = estimateTokens(prev.Content)
			return
		}
		if body == "" {
			return
		}
		ordinal++
		loc := anchor
		if loc == "" {
			loc = "paragraph " + strconv.Itoa(ordinal)
		}
		chunks = append(chunks, Chunk{
			Index:      len(chunks),
			Locator:    loc,
			Content:    body,
			TokenCount: estimateTokens(body),
		})
	}

	for _, p := range paragraphs {
		if isHeading(p) {
			// A heading belongs with what follows it, not with what came
			// before, so the boundary goes here.
			if buf.Len() >= minChars {
				flush()
			}
			heading = p
			// A chunk that has only collected a parent heading so far has
			// not really started, so the more specific heading claims it.
			// Otherwise a passage under "I. THE NECESSITY LIMB" would be
			// anchored to "ARGUMENTS ADVANCED", which helps nobody looking
			// for the quote.
			if buf.Len() < minChars {
				anchor = heading
			}
		}

		if buf.Len() == 0 {
			anchor = heading
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(p)

		if buf.Len() >= targetChars {
			carry := tail(buf.String(), overlapChars)
			flush()
			if carry != "" {
				buf.WriteString(carry)
				anchor = heading
			}
		}
	}
	flush()

	return chunks
}

// paragraphs splits on blank lines, then treats a run of single newlines as
// one block. PDF extraction produces both, often in the same document.
func paragraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []string
	for _, block := range strings.Split(text, "\n\n") {
		block = strings.TrimSpace(collapseSpaces(block))
		if block != "" {
			out = append(out, block)
		}
	}
	return out
}

// isHeading is a heuristic and says so. It catches numbered headings, short
// title-case lines and all-caps lines, which is what legal documents use.
func isHeading(p string) bool {
	if strings.Contains(p, "\n") || len(p) > maxHeadingChars || p == "" {
		return false
	}
	if numberedHeading(p) {
		return true
	}
	if strings.HasSuffix(p, ".") {
		return false
	}

	letters, upper := 0, 0
	for _, r := range p {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	// SHOUTED HEADING. Legal documents capitalise their section titles, and
	// nothing else in a memorial is both short and eighty percent upper case.
	return letters > 0 && upper*10 >= letters*8
}

// numberedHeading matches "3.", "3.2", "III.", "(a) Jurisdiction".
//
// The roman-numeral branch insists on a following "." or ")". Accepting a
// space too would make "Civil appeal dismissed" a heading, because its first
// five letters are all roman numerals.
func numberedHeading(p string) bool {
	r := []rune(p)
	if len(r) == 0 {
		return false
	}
	if r[0] == '(' || unicode.IsDigit(r[0]) {
		return true
	}

	i := 0
	for i < len(r) && strings.ContainsRune("IVXLCMivxlcm", r[i]) {
		i++
	}
	return i > 0 && i < len(r) && (r[i] == '.' || r[i] == ')')
}

// tail returns the last whole sentence or so of a chunk, to be carried into
// the next one. Cutting mid-word would poison the overlap it exists to help.
func tail(s string, n int) string {
	if len(s) <= n {
		return ""
	}
	cut := s[len(s)-n:]
	if i := strings.IndexAny(cut, ".!?\n"); i >= 0 && i+1 < len(cut) {
		cut = cut[i+1:]
	} else if i := strings.IndexByte(cut, ' '); i >= 0 {
		cut = cut[i+1:]
	}
	return strings.TrimSpace(cut)
}

func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteByte(' ')
			space = true
		case unicode.IsSpace(r):
			if !space {
				b.WriteByte(' ')
			}
			space = true
		default:
			b.WriteRune(r)
			space = false
		}
	}
	return b.String()
}

// estimateTokens is four characters to a token, which is close enough for a
// column nothing branches on. A real tokeniser here would mean shipping the
// model's vocabulary to the control plane for a statistic.
func estimateTokens(s string) int { return (len(s) + 3) / 4 }
