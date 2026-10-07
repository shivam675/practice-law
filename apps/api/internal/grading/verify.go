package grading

import (
	"strings"
	"unicode"
)

// verifyThreshold is the similarity a quote must reach to be shown to a
// student. Below it the quote is stored with verified = false and excluded
// from the report: a grade may still stand on the model's reasoning, but a
// sentence in quotation marks that nobody wrote must never reach a student.
const verifyThreshold = 0.9

// Verify scores how well a quote matches the source it claims to come from.
//
// Exact first, after normalising whitespace, because PDF extraction inserts
// line breaks and double spaces that mean nothing. Then fuzzy, because a
// model that drops an article or expands an ampersand has still found the
// right passage.
func Verify(quote, source string) float64 {
	q := normalise(quote)
	s := normalise(source)
	if q == "" || s == "" {
		return 0
	}
	if strings.Contains(s, q) {
		return 1
	}

	qw := strings.Fields(q)
	sw := strings.Fields(s)
	if len(qw) == 0 || len(sw) == 0 {
		return 0
	}
	// A quote longer than the source cannot be in it, and sliding a window
	// wider than the text would compare it against padding.
	if len(qw) > len(sw) {
		return ratio(q, s)
	}

	// Locate first, compare second. Character-level distance over a whole
	// memorial would be tens of millions of cell updates per quote; word
	// overlap narrows it to one window for the price of a single scan.
	window := len(qw)
	step := window / 8
	if step < 1 {
		step = 1
	}

	want := make(map[string]int, window)
	for _, w := range qw {
		want[w]++
	}

	bestStart, bestOverlap := 0, -1
	for start := 0; start+window <= len(sw); start += step {
		have := make(map[string]int, window)
		for _, w := range sw[start : start+window] {
			have[w]++
		}
		overlap := 0
		for w, n := range want {
			if m := have[w]; m < n {
				overlap += m
			} else {
				overlap += n
			}
		}
		if overlap > bestOverlap {
			bestStart, bestOverlap = start, overlap
		}
	}
	if bestOverlap <= 0 {
		return 0
	}

	// Widen the winning window by a word on each side: the quote may straddle
	// a boundary the step size landed between.
	lo := bestStart - 1
	if lo < 0 {
		lo = 0
	}
	hi := bestStart + window + 1
	if hi > len(sw) {
		hi = len(sw)
	}
	return ratio(q, strings.Join(sw[lo:hi], " "))
}

// ratio is normalised Levenshtein similarity, 0 to 1.
func ratio(a, b string) float64 {
	if a == b {
		return 1
	}
	longest := len(a)
	if len(b) > longest {
		longest = len(b)
	}
	if longest == 0 {
		return 1
	}
	return 1 - float64(distance(a, b))/float64(longest)
}

// distance is Levenshtein over bytes with a two-row buffer. Both inputs are
// normalised ASCII-ish text by the time they arrive, and a full matrix would
// allocate for nothing.
func distance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			best := prev[j] + 1
			if curr[j-1]+1 < best {
				best = curr[j-1] + 1
			}
			if prev[j-1]+cost < best {
				best = prev[j-1] + cost
			}
			curr[j] = best
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// normalise strips what document extraction adds and what citation style
// varies: case, runs of whitespace, and the punctuation that differs between
// a model's rendering of a quote and the source's own.
func normalise(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true // leading whitespace is dropped

	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			space = false
		case r == '\'' || r == '’':
			// Apostrophes stay: "State's" and "States" are different words.
			b.WriteByte('\'')
			space = false
		default:
			if !space {
				b.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}
