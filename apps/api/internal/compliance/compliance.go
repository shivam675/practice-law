// Package compliance checks a submitted document against a structural rule
// set.
//
// This is deliberately deterministic. Moot memorials are scored on structure,
// and structure is a rule: required sections, their order, and word limits. A
// regex answers that correctly, instantly and for free, where a language model
// would answer it expensively and inconsistently. The model grades content;
// this grades form.
package compliance

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

type Status string

const (
	StatusPass         Status = "pass"
	StatusFail         Status = "fail"
	StatusNotCheckable Status = "not_checkable"
)

// SectionRule describes one expected part of a document.
type SectionRule struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Aliases  []string `json:"aliases"`
	Required bool     `json:"required"`
	// MaxWords is counted over the section body, footnotes included, because
	// that is how competition rules count it. Zero means no limit.
	MaxWords int `json:"max_words,omitempty"`
}

type RuleSet struct {
	Key         string        `json:"key"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Sections    []SectionRule `json:"sections"`
	// MaxPages applies to the whole document and is only checkable when the
	// source reports a page count, which means PDF.
	MaxPages int `json:"max_pages,omitempty"`
	// AnonymousBody flags institution names outside the cover page, which most
	// competitions treat as a disqualifying identity leak.
	AnonymousBody bool `json:"anonymous_body"`
}

type Finding struct {
	Rule     string   `json:"rule"`
	Section  string   `json:"section,omitempty"`
	Status   Status   `json:"status"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Expected string   `json:"expected,omitempty"`
	Actual   string   `json:"actual,omitempty"`
}

type SectionFound struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Heading   string `json:"heading"`
	LineIndex int    `json:"line_index"`
	WordCount int    `json:"word_count"`
}

type Report struct {
	RuleSetKey string         `json:"rule_set_key"`
	Findings   []Finding      `json:"findings"`
	Sections   []SectionFound `json:"sections"`
	Passed     int            `json:"passed"`
	Failed     int            `json:"failed"`
	Warnings   int            `json:"warnings"`
	// Fraction in [0,1] of checkable rules that passed. The structure rubric
	// criterion multiplies its max score by this.
	Fraction float64 `json:"fraction"`
	// PageCount is 0 when the source could not report one.
	PageCount int `json:"page_count"`
	WordCount int `json:"word_count"`
}

// Input is the extracted document. Pages is 0 for formats that cannot report
// a reliable page count without rendering, which includes DOCX.
type Input struct {
	Text  string
	Pages int
}

// Check runs a rule set over an extracted document.
func Check(rs RuleSet, in Input) Report {
	lines := strings.Split(strings.ReplaceAll(in.Text, "\r\n", "\n"), "\n")

	found, order := detectSections(rs, lines)
	countWords(found, order, lines)

	rep := Report{
		RuleSetKey: rs.Key,
		PageCount:  in.Pages,
		WordCount:  countWordsIn(in.Text),
		Sections:   make([]SectionFound, 0, len(order)),
	}
	for _, key := range order {
		rep.Sections = append(rep.Sections, *found[key])
	}

	byKey := map[string]SectionRule{}
	for _, r := range rs.Sections {
		byKey[r.Key] = r
	}

	for _, rule := range rs.Sections {
		sec, ok := found[rule.Key]

		if !ok {
			if rule.Required {
				rep.Findings = append(rep.Findings, Finding{
					Rule: "section.missing", Section: rule.Key,
					Status: StatusFail, Severity: SeverityError,
					Message:  fmt.Sprintf("%s is required and was not found.", rule.Name),
					Expected: "present",
					Actual:   "absent",
				})
			}
			continue
		}

		rep.Findings = append(rep.Findings, Finding{
			Rule: "section.present", Section: rule.Key,
			Status: StatusPass, Severity: SeverityError,
			Message: fmt.Sprintf("%s found.", rule.Name),
			Actual:  sec.Heading,
		})

		if rule.MaxWords > 0 {
			status, severity := StatusPass, SeverityError
			msg := fmt.Sprintf("%s is within its %d word limit.", rule.Name, rule.MaxWords)
			if sec.WordCount > rule.MaxWords {
				status = StatusFail
				msg = fmt.Sprintf("%s exceeds its word limit by %d words.",
					rule.Name, sec.WordCount-rule.MaxWords)
			}
			rep.Findings = append(rep.Findings, Finding{
				Rule: "section.word_limit", Section: rule.Key,
				Status: status, Severity: severity, Message: msg,
				Expected: fmt.Sprintf("at most %d words", rule.MaxWords),
				Actual:   fmt.Sprintf("%d words", sec.WordCount),
			})
		}
	}

	rep.Findings = append(rep.Findings, checkOrder(rs, found, order)...)

	if rs.MaxPages > 0 {
		switch {
		case in.Pages == 0:
			rep.Findings = append(rep.Findings, Finding{
				Rule: "document.page_limit", Status: StatusNotCheckable, Severity: SeverityWarning,
				Message: "Page limit could not be checked because this format does not " +
					"report a reliable page count. Submit a PDF to have it checked.",
				Expected: fmt.Sprintf("at most %d pages", rs.MaxPages),
			})
		case in.Pages > rs.MaxPages:
			rep.Findings = append(rep.Findings, Finding{
				Rule: "document.page_limit", Status: StatusFail, Severity: SeverityError,
				Message:  fmt.Sprintf("The document is %d pages over the limit.", in.Pages-rs.MaxPages),
				Expected: fmt.Sprintf("at most %d pages", rs.MaxPages),
				Actual:   fmt.Sprintf("%d pages", in.Pages),
			})
		default:
			rep.Findings = append(rep.Findings, Finding{
				Rule: "document.page_limit", Status: StatusPass, Severity: SeverityError,
				Message:  "Within the page limit.",
				Expected: fmt.Sprintf("at most %d pages", rs.MaxPages),
				Actual:   fmt.Sprintf("%d pages", in.Pages),
			})
		}
	}

	if rs.AnonymousBody {
		rep.Findings = append(rep.Findings, checkAnonymity(found, order, lines))
	}

	for _, f := range rep.Findings {
		switch {
		case f.Status == StatusPass:
			rep.Passed++
		case f.Status == StatusFail && f.Severity == SeverityError:
			rep.Failed++
		case f.Severity == SeverityWarning:
			rep.Warnings++
		}
	}
	if total := rep.Passed + rep.Failed; total > 0 {
		rep.Fraction = float64(rep.Passed) / float64(total)
	} else {
		rep.Fraction = 1
	}

	return rep
}

var (
	// A heading is a short line. Anything long is prose that happens to
	// mention the words.
	maxHeadingLen = 90
	// Leading numbering: "3.", "III.", "PART B -".
	headingPrefix = regexp.MustCompile(`^\s*(?:part\s+[a-z0-9]+\s*[-:.]?\s*)?(?:[ivxlcdm]+|\d+)?\s*[.)\-:]?\s*`)
	nonLetter     = regexp.MustCompile(`[^a-z0-9 ]+`)
	spaces        = regexp.MustCompile(`\s+`)
)

func normaliseHeading(line string) string {
	// Word processors and PowerShell both emit a byte order mark, and PDF
	// extraction leaves zero-width characters behind. Either one silently
	// stops the first heading in a document from matching.
	s := strings.Map(func(r rune) rune {
		switch r {
		case '\uFEFF', '\u200B', '\u200C', '\u200D', '\u00A0':
			return -1
		}
		return r
	}, line)

	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, ".")
	s = headingPrefix.ReplaceAllString(s, "")
	s = nonLetter.ReplaceAllString(s, " ")
	s = spaces.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

type candidate struct {
	key       string
	name      string
	heading   string
	lineIndex int
}

// detectSections finds where each section actually begins.
//
// Section names appear at least twice in a real memorial: once as an entry in
// the table of contents and once as the heading itself. Taking the first match
// puts every section inside the contents list, which wrecks word counts and
// the order check.
//
// The distinguishing feature is what follows: a contents entry is followed by
// the next contents entry, while a real heading is followed by its body. So
// where a section matches several times, the occurrence with the most text
// before the next candidate wins.
func detectSections(rs RuleSet, lines []string) (map[string]*SectionFound, []string) {
	alias := map[string]SectionRule{}
	for _, rule := range rs.Sections {
		for _, a := range rule.Aliases {
			alias[normaliseHeading(a)] = rule
		}
		alias[normaliseHeading(rule.Name)] = rule
	}

	var candidates []candidate
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || len(line) > maxHeadingLen {
			continue
		}
		rule, ok := alias[normaliseHeading(line)]
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{
			key: rule.Key, name: rule.Name, heading: line, lineIndex: i,
		})
	}

	// Body size of each candidate: words between it and the next candidate.
	bodyWords := make([]int, len(candidates))
	for i, c := range candidates {
		end := len(lines)
		if i+1 < len(candidates) {
			end = candidates[i+1].lineIndex
		}
		start := c.lineIndex + 1
		if start > end {
			start = end
		}
		bodyWords[i] = countWordsIn(strings.Join(lines[start:end], " "))
	}

	best := map[string]int{}
	for i, c := range candidates {
		prev, seen := best[c.key]
		if !seen || bodyWords[i] > bodyWords[prev] {
			best[c.key] = i
		}
	}

	found := make(map[string]*SectionFound, len(best))
	for key, i := range best {
		found[key] = &SectionFound{
			Key: key, Name: candidates[i].name,
			Heading: candidates[i].heading, LineIndex: candidates[i].lineIndex,
		}
	}

	order := make([]string, 0, len(found))
	for key := range found {
		order = append(order, key)
	}
	sort.Slice(order, func(a, b int) bool {
		return found[order[a]].LineIndex < found[order[b]].LineIndex
	})
	return found, order
}

// countWords measures each section's body as the text up to the next detected
// heading.
func countWords(found map[string]*SectionFound, order []string, lines []string) {
	for i, key := range order {
		start := found[key].LineIndex + 1
		end := len(lines)
		if i+1 < len(order) {
			end = found[order[i+1]].LineIndex
		}
		if start > end {
			start = end
		}
		found[key].WordCount = countWordsIn(strings.Join(lines[start:end], " "))
	}
}

func countWordsIn(s string) int {
	return len(strings.Fields(s))
}

func checkOrder(rs RuleSet, found map[string]*SectionFound, order []string) []Finding {
	expected := make([]string, 0, len(rs.Sections))
	position := map[string]int{}
	for i, rule := range rs.Sections {
		position[rule.Key] = i
		if _, ok := found[rule.Key]; ok {
			expected = append(expected, rule.Key)
		}
	}
	if len(expected) < 2 {
		return nil
	}

	var misplaced []string
	for i := 1; i < len(order); i++ {
		if position[order[i]] < position[order[i-1]] {
			misplaced = append(misplaced, found[order[i]].Name)
		}
	}

	if len(misplaced) == 0 {
		return []Finding{{
			Rule: "document.section_order", Status: StatusPass, Severity: SeverityError,
			Message: "Sections appear in the required order.",
		}}
	}
	return []Finding{{
		Rule: "document.section_order", Status: StatusFail, Severity: SeverityError,
		Message: fmt.Sprintf("Sections are out of order: %s appear(s) too early.",
			strings.Join(misplaced, ", ")),
		Expected: strings.Join(expected, " then "),
		Actual:   strings.Join(order, " then "),
	}}
}

// identifyingWords are the terms that leak a team's institution. Matching is
// deliberately narrow: a false accusation of breaching anonymity is worse than
// a missed one, so this is a warning and a human decides.
var identifyingWords = regexp.MustCompile(
	`(?i)\b(university|universities|college|law school|institute of law|national law|nalsar|nlsiu|campus)\b`)

// caseName matches a line that reads as a citation. Case names routinely carry
// an institution ("Modern Dental College v. State of M.P.") and flagging those
// as an anonymity breach would make the check useless noise.
var caseName = regexp.MustCompile(`(?i)\s+vs?\.?\s+|\(\d{4}\)|\bAIR\b|\bSCC\b`)

func checkAnonymity(found map[string]*SectionFound, order []string, lines []string) Finding {
	// The cover page is allowed to carry a team code, and nothing before the
	// first detected heading is body text.
	start := 0
	if len(order) > 0 {
		start = found[order[0]].LineIndex
	}
	if _, ok := found["cover_page"]; ok && len(order) > 1 {
		start = found[order[1]].LineIndex
	}
	if start >= len(lines) {
		start = len(lines)
	}

	// The index of authorities is a list of case names and is skipped wholesale.
	skipFrom, skipTo := -1, -1
	if ioa, ok := found["index_of_authorities"]; ok {
		skipFrom = ioa.LineIndex
		skipTo = len(lines)
		for i, key := range order {
			if key == "index_of_authorities" && i+1 < len(order) {
				skipTo = found[order[i+1]].LineIndex
			}
		}
	}

	var hits []string
	for i := start; i < len(lines); i++ {
		if i >= skipFrom && i < skipTo {
			continue
		}
		if caseName.MatchString(lines[i]) {
			continue
		}
		for _, m := range identifyingWords.FindAllString(lines[i], -1) {
			hits = append(hits, fmt.Sprintf("line %d: %q", i+1, m))
			if len(hits) >= 5 {
				break
			}
		}
		if len(hits) >= 5 {
			break
		}
	}

	if len(hits) == 0 {
		return Finding{
			Rule: "document.anonymity", Status: StatusPass, Severity: SeverityError,
			Message: "No institutional identifiers found in the body.",
		}
	}
	return Finding{
		Rule: "document.anonymity", Status: StatusFail, Severity: SeverityWarning,
		Message: "Possible institutional identifier in the body. A human should confirm " +
			"before this affects a score.",
		Actual: strings.Join(hits, "; "),
	}
}
