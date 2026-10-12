package grading

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/slmlabs/megamoot/apps/api/internal/rubrics"
)

func TestExcerptKeepsShortRecordsWhole(t *testing.T) {
	text := "[written]\nA short memorial."
	if excerpt(text, rubrics.Criterion{Name: "Authorities"}) != text {
		t.Fatal("a record within budget must not be cut")
	}
}

func TestExcerptSelectsRelevantVerbatimPassages(t *testing.T) {
	filler := strings.Repeat("न्याय The weather was pleasant and the hall was full of people.\n", 400)
	text := "[written]\nIntroduction to the memorial.\n" + filler +
		"Kesavananda Bharati is the controlling authority on the basic structure.\n" +
		"[oral]\nCounsel: May it please the court.\n" + filler
	got := excerpt(text, rubrics.Criterion{Name: "Authorities", Description: "Use of controlling authority and precedent"})
	if len(got) > recordBudget+1000 || !utf8.ValidString(got) {
		t.Fatalf("excerpt is %d bytes or invalid UTF-8", len(got))
	}
	for _, want := range []string{"Introduction to the memorial.", "Counsel: May it please the court.", "controlling authority on the basic structure"} {
		if !strings.Contains(got, want) {
			t.Fatalf("excerpt lost %q", want)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "[...]" && !strings.HasPrefix(line, "Excerpts of the record") && line != "Headings in the full record:" && !strings.Contains(text, line) {
			t.Fatalf("excerpt invented text: %q", line)
		}
	}
}

func TestExcerptCarriesHeadingsOfOmittedPassages(t *testing.T) {
	body := strings.Repeat("The respondent relies on the general rule stated in the record.\n", 300)
	text := "[written]\nISSUE I: JURISDICTION OF THE COURT\n" + body + "ISSUE II: CONCLUSIVENESS OF THE FOREIGN JUDGMENT\n" + body
	got := excerpt(text, rubrics.Criterion{Name: "Structure", Description: "Organisation and signposting"})
	if !strings.Contains(got, "ISSUE II: CONCLUSIVENESS OF THE FOREIGN JUDGMENT") {
		t.Fatal("a structure criterion cannot see the document's headings")
	}
}
