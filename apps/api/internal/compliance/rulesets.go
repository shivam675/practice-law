package compliance

import "fmt"

// Built-in rule sets. Limits follow published competition rules, but every
// organiser varies them, so a teacher overrides them per assessment rather
// than arguing with a default.
//
// Per-section page limits are not modelled: a page count cannot be derived
// from extracted text, and inferring one would produce confident wrong
// findings on a graded submission. Word limits are checked exactly; a
// whole-document page cap is checked when the source is a PDF.
const (
	RuleSetIndianNational = "indian_national_v1"
	RuleSetJessup         = "jessup_v1"
)

func indianNational() RuleSet {
	return RuleSet{
		Key:  RuleSetIndianNational,
		Name: "Indian National Moot Court (standard)",
		Description: "Cover page through prayer, as used by most Indian national " +
			"rounds. Word limits are the organiser's; override them per assessment.",
		MaxPages:      40,
		AnonymousBody: true,
		Sections: []SectionRule{
			{Key: "cover_page", Name: "Cover Page", Required: true,
				Aliases: []string{"cover page", "cause title", "before the honourable supreme court"}},
			{Key: "table_of_contents", Name: "Table of Contents", Required: true,
				Aliases: []string{"table of contents", "contents", "index"}},
			{Key: "index_of_authorities", Name: "Index of Authorities", Required: true,
				Aliases: []string{"index of authorities", "table of authorities", "list of authorities"}},
			{Key: "statement_of_jurisdiction", Name: "Statement of Jurisdiction", Required: true,
				Aliases: []string{"statement of jurisdiction", "jurisdiction"}},
			{Key: "statement_of_facts", Name: "Statement of Facts", Required: true, MaxWords: 1200,
				Aliases: []string{"statement of facts", "statement of fact", "facts of the case", "facts"}},
			{Key: "statement_of_issues", Name: "Statement of Issues", Required: true,
				Aliases: []string{"statement of issues", "issues raised", "issues for consideration",
					"questions presented", "issues"}},
			{Key: "summary_of_arguments", Name: "Summary of Arguments", Required: true, MaxWords: 800,
				Aliases: []string{"summary of arguments", "summary of pleadings", "summary of submissions"}},
			{Key: "arguments_advanced", Name: "Arguments Advanced", Required: true, MaxWords: 6000,
				Aliases: []string{"arguments advanced", "pleadings", "arguments", "submissions"}},
			{Key: "prayer", Name: "Prayer", Required: true, MaxWords: 300,
				Aliases: []string{"prayer", "prayer for relief", "relief sought", "conclusion and prayer"}},
		},
	}
}

func jessup() RuleSet {
	return RuleSet{
		Key:  RuleSetJessup,
		Name: "Jessup (ILSA) memorial",
		Description: "Structure and word limits following the published Jessup rules: " +
			"pleadings 10,000 words including footnotes and prayer, summary of " +
			"pleadings 800, statement of facts 1,300.",
		AnonymousBody: true,
		Sections: []SectionRule{
			{Key: "cover_page", Name: "Cover Page", Required: true,
				Aliases: []string{"cover page", "in the international court of justice"}},
			{Key: "table_of_contents", Name: "Table of Contents", Required: true,
				Aliases: []string{"table of contents", "contents"}},
			{Key: "index_of_authorities", Name: "Index of Authorities", Required: true,
				Aliases: []string{"index of authorities", "table of authorities"}},
			{Key: "statement_of_jurisdiction", Name: "Statement of Jurisdiction", Required: true,
				Aliases: []string{"statement of jurisdiction", "jurisdiction"}},
			{Key: "questions_presented", Name: "Questions Presented", Required: true,
				Aliases: []string{"questions presented", "question presented", "issues presented"}},
			{Key: "statement_of_facts", Name: "Statement of Facts", Required: true, MaxWords: 1300,
				Aliases: []string{"statement of facts", "statement of fact", "facts"}},
			{Key: "summary_of_pleadings", Name: "Summary of Pleadings", Required: true, MaxWords: 800,
				Aliases: []string{"summary of pleadings", "summary of arguments"}},
			{Key: "pleadings", Name: "Pleadings", Required: true, MaxWords: 10000,
				Aliases: []string{"pleadings", "arguments advanced", "arguments"}},
			{Key: "prayer", Name: "Prayer for Relief", Required: true,
				Aliases: []string{"prayer for relief", "prayer", "conclusion and prayer", "conclusion"}},
		},
	}
}

var builtIn = map[string]func() RuleSet{
	RuleSetIndianNational: indianNational,
	RuleSetJessup:         jessup,
	// Alias kept because the seeded template refers to it.
	"moot_memorial_v1": indianNational,
}

// Lookup returns a built-in rule set by key.
func Lookup(key string) (RuleSet, error) {
	build, ok := builtIn[key]
	if !ok {
		return RuleSet{}, fmt.Errorf("compliance: unknown rule set %q", key)
	}
	return build(), nil
}

// Keys lists the rule sets a template may name.
func Keys() []string {
	return []string{RuleSetIndianNational, RuleSetJessup}
}
