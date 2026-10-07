package retrieval

import (
	"strings"
	"testing"
)

const memorial = `MEMORIAL FOR THE APPLICANT

STATEMENT OF FACTS

On 4 August the State issued an order under section 144 suspending telecom
services across the district. The order recited a threat to public order in
general terms and was renewed four times on an identical recital.

ARGUMENTS ADVANCED

I. THE SUSPENSION FAILS THE NECESSITY LIMB

The proportionality standard requires the State to show that no less
restrictive measure was available. A recital repeated without amendment cannot
discharge that burden, because the facts it recites were never re-examined.

In Anuradha Bhasin v Union of India this Court held that an indefinite
suspension is impermissible and that every order must be subjected to periodic
review by a competent review committee.

II. THE ORDERS WERE NOT PUBLISHED

Publication is a precondition to challenge. An unpublished order forecloses
the remedy that makes the right meaningful.

PRAYER

The applicant prays that the orders be quashed.`

func TestSplitAnchorsChunksToTheNearestHeading(t *testing.T) {
	chunks := Split(memorial)
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}

	for i, c := range chunks {
		if c.Index != i {
			t.Fatalf("chunk %d has Index %d", i, c.Index)
		}
		if c.Locator == "" {
			t.Fatalf("chunk %d has no locator", i)
		}
		if strings.TrimSpace(c.Content) == "" {
			t.Fatalf("chunk %d is empty", i)
		}
	}

	// The passage a grader will quote must be anchored to a heading a student
	// can find, not to "paragraph 3".
	var found bool
	for _, c := range chunks {
		if strings.Contains(c.Content, "Anuradha Bhasin") {
			found = true
			if !strings.Contains(c.Locator, "NECESSITY") {
				t.Fatalf("the Bhasin passage is anchored to %q", c.Locator)
			}
		}
	}
	if !found {
		t.Fatal("the Bhasin passage did not survive chunking")
	}
}

func TestSplitKeepsAllTheText(t *testing.T) {
	chunks := Split(memorial)

	var joined strings.Builder
	for _, c := range chunks {
		joined.WriteString(c.Content)
		joined.WriteString(" ")
	}
	all := joined.String()

	for _, phrase := range []string{
		"section 144", "periodic review", "Publication is a precondition",
		"the orders be quashed",
	} {
		if !strings.Contains(all, phrase) {
			t.Errorf("chunking dropped %q", phrase)
		}
	}
}

func TestSplitOfEmptyOrTinyInput(t *testing.T) {
	if got := Split(""); got != nil {
		t.Fatalf("Split(\"\") = %v", got)
	}
	if got := Split("   \n\n  \n"); got != nil {
		t.Fatalf("whitespace produced %v", got)
	}
	// A single short paragraph is still worth one chunk; the minimum size
	// rule only merges a fragment into a chunk that already exists.
	if got := Split("A short note."); len(got) != 1 {
		t.Fatalf("one paragraph produced %d chunks", len(got))
	}
}

func TestLongDocumentIsSplitAndOverlapped(t *testing.T) {
	var b strings.Builder
	b.WriteString("ARGUMENTS ADVANCED\n\n")
	for i := 0; i < 40; i++ {
		b.WriteString("The impugned order is disproportionate because the " +
			"record discloses no consideration of a narrower measure.\n\n")
	}

	chunks := Split(b.String())
	if len(chunks) < 3 {
		t.Fatalf("a long document produced only %d chunks", len(chunks))
	}
	for _, c := range chunks {
		if len(c.Content) > targetChars*2 {
			t.Fatalf("chunk of %d chars blew past the target", len(c.Content))
		}
		if c.TokenCount == 0 {
			t.Fatal("token count not estimated")
		}
	}
}

func TestHeadingDetection(t *testing.T) {
	headings := []string{
		"STATEMENT OF FACTS",
		"I. THE SUSPENSION FAILS",
		"3.2",
		"(a) Jurisdiction",
		"ARGUMENTS ADVANCED",
	}
	for _, h := range headings {
		if !isHeading(h) {
			t.Errorf("isHeading(%q) = false", h)
		}
	}

	body := []string{
		"The proportionality standard requires the State to show that no less restrictive measure was available.",
		"",
		"In Anuradha Bhasin this Court held an indefinite suspension impermissible and required periodic review of every order made under the section.",
	}
	for _, p := range body {
		if isHeading(p) {
			t.Errorf("isHeading(%q) = true", p)
		}
	}
}
