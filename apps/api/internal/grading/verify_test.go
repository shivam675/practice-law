package grading

import "testing"

const source = `ARGUMENTS ADVANCED

I. THE SUSPENSION FAILS THE NECESSITY LIMB

The  proportionality standard requires the State to show that no
less restrictive measure was available. A recital repeated without
amendment cannot discharge that burden.

In Anuradha Bhasin v Union of India this Court held that an indefinite
suspension is impermissible and that every order must be subjected to
periodic review by a competent review committee.`

func TestExactQuoteVerifiesDespiteExtractionWhitespace(t *testing.T) {
	// The source has a double space and a line break inside this sentence.
	quote := "The proportionality standard requires the State to show that no less restrictive measure was available."
	if got := Verify(quote, source); got != 1 {
		t.Fatalf("Verify = %.3f, want 1", got)
	}
}

func TestNearQuoteClearsTheThreshold(t *testing.T) {
	// A dropped article and an expanded abbreviation: the model found the
	// right passage and rendered it slightly differently.
	quote := "In Anuradha Bhasin versus Union of India this Court held that indefinite suspension is impermissible and that every order must be subjected to periodic review by a competent review committee."
	got := Verify(quote, source)
	if got < verifyThreshold {
		t.Fatalf("Verify = %.3f, want at least %.2f", got, verifyThreshold)
	}
}

// The failure this whole mechanism exists for.
func TestHallucinatedQuoteFails(t *testing.T) {
	quote := "This Court has consistently held that administrative convenience alone justifies a continuing restriction on speech."
	got := Verify(quote, source)
	if got >= verifyThreshold {
		t.Fatalf("Verify = %.3f: a fabricated quote cleared the threshold", got)
	}
}

// A quote assembled from real words of the source but never written as a
// sentence is the subtle case: word overlap alone would pass it.
func TestRearrangedWordsFail(t *testing.T) {
	quote := "every order requires the State to show periodic review was available without amendment to discharge the burden"
	got := Verify(quote, source)
	if got >= verifyThreshold {
		t.Fatalf("Verify = %.3f: a rearranged quote cleared the threshold", got)
	}
}

func TestEmptyInputs(t *testing.T) {
	if got := Verify("", source); got != 0 {
		t.Fatalf("empty quote scored %.3f", got)
	}
	if got := Verify("anything", ""); got != 0 {
		t.Fatalf("empty source scored %.3f", got)
	}
}

func TestQuoteLongerThanTheSourceDoesNotPanic(t *testing.T) {
	long := source + source + source
	if got := Verify(long, "A recital repeated without amendment."); got >= verifyThreshold {
		t.Fatalf("Verify = %.3f", got)
	}
}

func TestCaseAndPunctuationDoNotMatter(t *testing.T) {
	quote := `"A RECITAL, REPEATED WITHOUT AMENDMENT, CANNOT DISCHARGE THAT BURDEN!"`
	if got := Verify(quote, source); got != 1 {
		t.Fatalf("Verify = %.3f, want 1", got)
	}
}
