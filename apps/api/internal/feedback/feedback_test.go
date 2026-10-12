package feedback

import (
	"strings"
	"testing"
)

func TestInputValidation(t *testing.T) {
	good := func() Input {
		return Input{Rating: 4, Relevance: "partly", Interruptions: "too_many", Problems: []string{"audio"}, Comment: " fine "}
	}
	for name, test := range map[string]struct {
		edit  func(*Input)
		valid bool
	}{
		"good":            {func(*Input) {}, true},
		"no problems":     {func(in *Input) { in.Problems = nil }, true},
		"rating zero":     {func(in *Input) { in.Rating = 0 }, false},
		"rating six":      {func(in *Input) { in.Rating = 6 }, false},
		"relevance":       {func(in *Input) { in.Relevance = "maybe" }, false},
		"interruptions":   {func(in *Input) { in.Interruptions = "" }, false},
		"unknown problem": {func(in *Input) { in.Problems = []string{"wifi"} }, false},
		"repeat problem":  {func(in *Input) { in.Problems = []string{"other", "other"} }, false},
		"long comment":    {func(in *Input) { in.Comment = strings.Repeat("é", 2001) }, false},
		"2000 runes":      {func(in *Input) { in.Comment = strings.Repeat("é", 2000) }, true},
	} {
		in := good()
		test.edit(&in)
		if err := in.validate(); (err == nil) != test.valid {
			t.Fatalf("%s: validity should be %v, got %v", name, test.valid, err)
		}
		if test.valid && in.Problems == nil {
			t.Fatalf("%s: problems must normalise to an empty list", name)
		}
	}
	in := good()
	if err := in.validate(); err != nil || in.Comment != "fine" {
		t.Fatalf("comment should be trimmed, got %q (%v)", in.Comment, err)
	}
}
