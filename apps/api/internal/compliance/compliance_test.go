package compliance

import (
	"strings"
	"testing"
)

// A compliant memorial skeleton. Section bodies are short filler; the checker
// measures structure and length, not meaning.
func memorial(overrides map[string]string) string {
	body := func(words int) string {
		return strings.TrimSpace(strings.Repeat("submission ", words))
	}
	sections := []struct{ heading, text string }{
		{"COVER PAGE", "Team Code TC-14\nBefore the Supreme Court of Ardhanari"},
		{"TABLE OF CONTENTS", "Index of Authorities ... 2\nStatement of Jurisdiction ... 3"},
		{"INDEX OF AUTHORITIES", "Anuradha Bhasin v. Union of India, (2020) 3 SCC 637"},
		{"STATEMENT OF JURISDICTION", "The Respondents approach this Court under Article 136."},
		{"STATEMENT OF FACTS", body(60)},
		{"STATEMENT OF ISSUES", "I. Whether the suspension was proportionate."},
		{"SUMMARY OF ARGUMENTS", body(40)},
		{"ARGUMENTS ADVANCED", body(200)},
		{"PRAYER", "Wherefore it is prayed that this Court may set aside the order."},
	}

	var b strings.Builder
	for _, s := range sections {
		text := s.text
		if override, ok := overrides[s.heading]; ok {
			text = override
		}
		b.WriteString(s.heading + "\n" + text + "\n\n")
	}
	return b.String()
}

func findingFor(rep Report, rule, section string) (Finding, bool) {
	for _, f := range rep.Findings {
		if f.Rule == rule && (section == "" || f.Section == section) {
			return f, true
		}
	}
	return Finding{}, false
}

func TestCompliantMemorialPasses(t *testing.T) {
	rs, err := Lookup(RuleSetIndianNational)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}

	rep := Check(rs, Input{Text: memorial(nil), Pages: 22})

	if rep.Failed != 0 {
		for _, f := range rep.Findings {
			if f.Status == StatusFail {
				t.Logf("unexpected failure: %s %s: %s", f.Rule, f.Section, f.Message)
			}
		}
		t.Fatalf("expected no failures, got %d", rep.Failed)
	}
	if len(rep.Sections) != 9 {
		t.Fatalf("expected 9 detected sections, got %d", len(rep.Sections))
	}
	if rep.Fraction != 1 {
		t.Fatalf("expected a perfect fraction, got %v", rep.Fraction)
	}
}

func TestMissingSectionFails(t *testing.T) {
	rs, _ := Lookup(RuleSetIndianNational)
	text := strings.Replace(memorial(nil), "INDEX OF AUTHORITIES\n", "", 1)

	rep := Check(rs, Input{Text: text, Pages: 20})

	f, ok := findingFor(rep, "section.missing", "index_of_authorities")
	if !ok {
		t.Fatal("expected a missing-section finding for the index of authorities")
	}
	if f.Status != StatusFail || f.Severity != SeverityError {
		t.Fatalf("expected a hard failure, got %v/%v", f.Status, f.Severity)
	}
	if rep.Fraction >= 1 {
		t.Fatal("a missing required section must reduce the fraction")
	}
}

func TestWordLimitIsEnforcedPerSection(t *testing.T) {
	rs, _ := Lookup(RuleSetIndianNational)
	long := strings.TrimSpace(strings.Repeat("argument ", 1500))

	rep := Check(rs, Input{Text: memorial(map[string]string{"STATEMENT OF FACTS": long}), Pages: 20})

	f, ok := findingFor(rep, "section.word_limit", "statement_of_facts")
	if !ok {
		t.Fatal("expected a word-limit finding for the statement of facts")
	}
	if f.Status != StatusFail {
		t.Fatalf("expected the word limit to fail, got %v (%s)", f.Status, f.Actual)
	}
	if !strings.Contains(f.Message, "exceeds its word limit") {
		t.Fatalf("unhelpful message: %q", f.Message)
	}
}

func TestSectionOrderIsChecked(t *testing.T) {
	rs, _ := Lookup(RuleSetIndianNational)

	// Prayer moved ahead of the arguments.
	text := strings.Replace(memorial(nil), "PRAYER\n", "", 1)
	text = strings.Replace(text, "ARGUMENTS ADVANCED\n",
		"PRAYER\nWherefore it is prayed.\n\nARGUMENTS ADVANCED\n", 1)

	rep := Check(rs, Input{Text: text, Pages: 20})

	f, ok := findingFor(rep, "document.section_order", "")
	if !ok {
		t.Fatal("expected a section order finding")
	}
	if f.Status != StatusFail {
		t.Fatalf("expected the order check to fail, got %v", f.Status)
	}
}

func TestPageLimitIsNotCheckableWithoutAPageCount(t *testing.T) {
	rs, _ := Lookup(RuleSetIndianNational)

	rep := Check(rs, Input{Text: memorial(nil), Pages: 0})

	f, ok := findingFor(rep, "document.page_limit", "")
	if !ok {
		t.Fatal("expected a page limit finding")
	}
	if f.Status != StatusNotCheckable {
		t.Fatalf("expected not_checkable without a page count, got %v", f.Status)
	}
	// An uncheckable rule must not be counted as a failure against a student.
	if rep.Failed != 0 {
		t.Fatalf("an uncheckable rule must not fail the submission, got %d failures", rep.Failed)
	}
}

func TestPageLimitFailsWhenExceeded(t *testing.T) {
	rs, _ := Lookup(RuleSetIndianNational)

	rep := Check(rs, Input{Text: memorial(nil), Pages: 55})

	f, _ := findingFor(rep, "document.page_limit", "")
	if f.Status != StatusFail {
		t.Fatalf("expected the page limit to fail at 55 pages, got %v", f.Status)
	}
}

func TestAnonymityBreachIsAWarningNotAFailure(t *testing.T) {
	rs, _ := Lookup(RuleSetIndianNational)
	text := memorial(map[string]string{
		"ARGUMENTS ADVANCED": "Counsel for the appellant, National Law University, submits.",
	})

	rep := Check(rs, Input{Text: text, Pages: 20})

	f, ok := findingFor(rep, "document.anonymity", "")
	if !ok {
		t.Fatal("expected an anonymity finding")
	}
	if f.Status != StatusFail || f.Severity != SeverityWarning {
		t.Fatalf("expected a warning-level failure, got %v/%v", f.Status, f.Severity)
	}
	if rep.Warnings != 1 {
		t.Fatalf("expected exactly 1 warning, got %d", rep.Warnings)
	}
	// A warning is for a human to resolve; it must not reduce a score on its own.
	if rep.Failed != 0 {
		t.Fatalf("a warning must not count as a failure, got %d", rep.Failed)
	}
}

// A real memorial names every section twice: once in the table of contents and
// once as the heading. Matching the contents entry puts the whole document
// inside the contents list and wrecks every word count.
func TestTableOfContentsEntriesDoNotShadowRealHeadings(t *testing.T) {
	rs, _ := Lookup(RuleSetIndianNational)

	text := memorial(map[string]string{
		"TABLE OF CONTENTS": strings.Join([]string{
			"Index of Authorities",
			"Statement of Jurisdiction",
			"Statement of Facts",
			"Arguments Advanced",
			"Prayer",
		}, "\n"),
	})

	rep := Check(rs, Input{Text: text, Pages: 20})

	index := map[string]SectionFound{}
	for _, s := range rep.Sections {
		index[s.Key] = s
	}

	toc := index["table_of_contents"]
	for _, key := range []string{"index_of_authorities", "statement_of_jurisdiction",
		"statement_of_facts", "arguments_advanced", "prayer"} {
		got, ok := index[key]
		if !ok {
			t.Fatalf("%s was not detected at all", key)
		}
		if got.LineIndex <= toc.LineIndex+6 {
			t.Errorf("%s was detected at line %d, inside the table of contents at %d",
				key, got.LineIndex, toc.LineIndex)
		}
	}

	if args := index["arguments_advanced"]; args.WordCount < 100 {
		t.Errorf("arguments advanced counted %d words; it was matched in the contents list",
			args.WordCount)
	}
	if rep.Failed != 0 {
		for _, f := range rep.Findings {
			if f.Status == StatusFail {
				t.Logf("unexpected failure: %s %s: %s", f.Rule, f.Section, f.Message)
			}
		}
		t.Fatalf("a memorial with a real contents list reported %d failures", rep.Failed)
	}
}

// Word processors and several extraction paths prefix the first line with a
// byte order mark, which silently stops the first heading matching.
func TestLeadingByteOrderMarkDoesNotHideTheFirstHeading(t *testing.T) {
	rs, _ := Lookup(RuleSetIndianNational)

	const bom = ""
	rep := Check(rs, Input{Text: bom + memorial(nil), Pages: 20})

	if _, ok := findingFor(rep, "section.present", "cover_page"); !ok {
		t.Fatal("the cover page was not detected because of a byte order mark")
	}
}

// Case names carry institution names. Flagging them would make the anonymity
// check noise a teacher learns to ignore.
func TestCitationsAreNotTreatedAsAnonymityBreaches(t *testing.T) {
	rs, _ := Lookup(RuleSetIndianNational)

	text := memorial(map[string]string{
		"INDEX OF AUTHORITIES": "Modern Dental College v. State of M.P., (2016) 7 SCC 353",
		"ARGUMENTS ADVANCED": "The test in Modern Dental College v. State of M.P., " +
			"(2016) 7 SCC 353 applies. " + strings.TrimSpace(strings.Repeat("submission ", 150)),
	})

	rep := Check(rs, Input{Text: text, Pages: 20})

	f, _ := findingFor(rep, "document.anonymity", "")
	if f.Status != StatusPass {
		t.Fatalf("a cited case name was flagged as an anonymity breach: %s", f.Actual)
	}
}

func TestHeadingVariantsAreAccepted(t *testing.T) {
	rs, _ := Lookup(RuleSetIndianNational)

	// Numbered, differently cased and aliased headings all occur in practice.
	text := memorial(nil)
	text = strings.Replace(text, "INDEX OF AUTHORITIES", "3. Table of Authorities", 1)
	text = strings.Replace(text, "SUMMARY OF ARGUMENTS", "VII. Summary of Pleadings", 1)
	text = strings.Replace(text, "ARGUMENTS ADVANCED", "Pleadings", 1)

	rep := Check(rs, Input{Text: text, Pages: 20})
	for _, key := range []string{"index_of_authorities", "summary_of_arguments", "arguments_advanced"} {
		if _, ok := findingFor(rep, "section.present", key); !ok {
			t.Errorf("heading variant for %q was not recognised", key)
		}
	}
}

func TestJessupLimitsDiffer(t *testing.T) {
	rs, err := Lookup(RuleSetJessup)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	for _, s := range rs.Sections {
		if s.Key == "pleadings" && s.MaxWords != 10000 {
			t.Fatalf("jessup pleadings limit = %d, want 10000", s.MaxWords)
		}
		if s.Key == "statement_of_facts" && s.MaxWords != 1300 {
			t.Fatalf("jessup facts limit = %d, want 1300", s.MaxWords)
		}
	}
}

func TestUnknownRuleSetIsRefused(t *testing.T) {
	if _, err := Lookup("vibes_v1"); err == nil {
		t.Fatal("expected an unknown rule set to be refused")
	}
}

func TestSeededTemplateRuleSetResolves(t *testing.T) {
	// The seeded moot template names this key; if it stops resolving, every
	// seeded submission silently skips its structure check.
	if _, err := Lookup("moot_memorial_v1"); err != nil {
		t.Fatalf("the seeded rule set key must resolve: %v", err)
	}
}
