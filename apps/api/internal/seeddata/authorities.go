package seeddata

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// source is a seeded piece of assessment material.
//
// The text is stored inline with parse_status 'parsed' rather than uploaded as
// a blob, so retrieval has something real to chunk before the ingestion
// pipeline exists. Real assessments upload documents through the normal path.
type source struct {
	Kind       string // problem | authority | statute | guidance
	Title      string
	Citation   string
	Visibility string
	Body       string
}

// Authority summaries are written here, not copied. Indian Supreme Court
// judgments are public under section 52(1)(q) of the Copyright Act, 1957, but
// full texts belong in the ingestion pipeline with a named source, not in a
// seed file.
func mootSources() []source {
	return []source{
		{
			Kind: "problem", Title: "Moot Problem: Telecom Suspension in the State of Devgarh",
			Visibility: "all",
			Body: `MOOT PROBLEM
The State of Devgarh v. Kiran Rao & Anr.
Before the Supreme Court of Ardhanari, Civil Appellate Jurisdiction

STATEMENT OF FACTS

1. The Union of Ardhanari is a federal democratic republic. Its Constitution
   guarantees, at Article 19(1)(a), the freedom of speech and expression,
   subject to reasonable restrictions under Article 19(2), and at Article 21,
   the right to life and personal liberty.

2. The State of Devgarh is an industrial state with a population of
   approximately 40 million. Between 3 and 9 March, protests over a proposed
   land acquisition statute took place in four districts. Police records note
   sporadic stone-throwing on 5 March and damage to two public buses. No
   fatality was recorded in any district at any point.

3. On 6 March, the Home Secretary of Devgarh issued an order under Rule 2 of
   the Temporary Suspension of Telecom Services Rules suspending mobile
   internet services across the entire State for an initial period of seven
   days. The order recited "apprehension of public disorder arising from
   circulation of inflammatory content" and was neither published nor served
   on any telecom licensee's subscribers.

4. The order was renewed four times. Mobile internet remained suspended for
   48 consecutive days. Fixed-line broadband was unaffected in the capital but
   is available to roughly 6 per cent of households statewide.

5. The Respondent, Ms Kiran Rao, operates a tutoring service delivering all
   instruction online to approximately 1,400 students, of whom 300 were
   preparing for State board examinations held on 14 April. Her business
   ceased operating for the duration. The Second Respondent is a registered
   society of community health workers who report that antenatal teleconsults
   in two districts fell by 71 per cent during the suspension.

6. The Respondents petitioned the High Court of Devgarh. The State produced
   the suspension orders in a sealed cover and declined to place the Review
   Committee's minutes on record, asserting that disclosure would compromise
   ongoing operations. The High Court held the suspension unconstitutional,
   directed publication of all orders, and awarded no damages.

7. The State of Devgarh appeals. The Respondents have filed cross-objections
   seeking compensation and a direction that every future suspension order be
   published within 24 hours of issue.

ISSUES RAISED

I.   Whether a statewide suspension of mobile internet services, renewed for
     48 days on an identical recital, satisfies the requirement of
     proportionality under Articles 19(2) and 21.

II.  Whether the non-publication of suspension orders and the withholding of
     Review Committee minutes is sustainable, and what consequence follows.

III. Whether the right to carry on trade through the internet, and the right
     to access health and education services online, attract constitutional
     protection independent of Article 19(1)(a).

IV.  Whether monetary compensation is an available remedy for an
     unconstitutional suspension, and if so on what basis it is assessed.

INSTRUCTIONS TO COUNSEL

Counsel for the Appellant State must justify the suspension and resist the
cross-objections. Counsel for the Respondents must defend the High Court's
findings and press the cross-objections. Both sides will be heard on all four
issues. Speaking time is allotted per speaker and is strictly enforced.

Nothing in this problem should be taken as a statement of the law of any real
jurisdiction. The State of Devgarh and the Union of Ardhanari are fictional.`,
		},
		{
			Kind: "statute", Title: "Constitutional Provisions in Issue", Visibility: "all",
			Body: `EXTRACTS SUPPLIED TO COUNSEL

Article 19(1)(a): All citizens shall have the right to freedom of speech and
expression.

Article 19(1)(g): All citizens shall have the right to practise any
profession, or to carry on any occupation, trade or business.

Article 19(2): Nothing in sub-clause (a) of clause (1) shall affect the
operation of any existing law, or prevent the State from making any law, in so
far as such law imposes reasonable restrictions on the exercise of the right
conferred by the said sub-clause in the interests of the sovereignty and
integrity of India, the security of the State, friendly relations with foreign
States, public order, decency or morality, or in relation to contempt of
court, defamation or incitement to an offence.

Article 19(6): Nothing in sub-clause (g) of clause (1) shall affect the
operation of any existing law in so far as it imposes, or prevent the State
from making any law imposing, in the interests of the general public,
reasonable restrictions on the exercise of the right conferred by the said
sub-clause.

Article 21: No person shall be deprived of his life or personal liberty except
according to procedure established by law.

Temporary Suspension of Telecom Services (Public Emergency or Public Safety)
Rules, 2017, Rule 2: suspension orders may be issued by a competent authority,
must record reasons, and are subject to review by a Review Committee within
five working days.`,
		},
		{
			Kind: "authority", Title: "Maneka Gandhi v. Union of India",
			Citation: "(1978) 1 SCC 248", Visibility: "all",
			Body: `Supreme Court of India. Seven-judge bench.

The passport of the petitioner was impounded without reasons being supplied
and without a hearing. The Court held that a procedure established by law
under Article 21 must be right, just and fair, and not arbitrary, fanciful or
oppressive.

Significance for this problem: Articles 14, 19 and 21 are not mutually
exclusive compartments. A law or executive action depriving liberty must
survive scrutiny under each. The reasonableness of a restriction is a judicial
question, not a matter of executive satisfaction.`,
		},
		{
			Kind: "authority", Title: "Modern Dental College & Research Centre v. State of Madhya Pradesh",
			Citation: "(2016) 7 SCC 353", Visibility: "all",
			Body: `Supreme Court of India. Constitution bench.

The Court set out proportionality as the test for whether a restriction is
reasonable, identifying four requirements: the measure must pursue a
legitimate goal; it must be rationally connected to that goal; there must be
no less restrictive but equally effective alternative; and the measure must
strike a proper balance between the harm to the right and the benefit secured.

Significance for this problem: this is the structure within which Issue I is
argued. A party relying on it should address all four limbs; omitting the
least-restrictive-alternative limb is the most common error.`,
		},
		{
			Kind: "authority", Title: "K.S. Puttaswamy v. Union of India",
			Citation: "(2017) 10 SCC 1", Visibility: "all",
			Body: `Supreme Court of India. Nine-judge bench.

The Court held privacy to be a fundamental right protected under Article 21
and as part of the freedoms under Part III, and confirmed proportionality as
the standard governing limitations on it.

Significance for this problem: supports the contention that rights exercised
through a medium are protected independently of the medium, and reinforces the
proportionality framework relied on in Modern Dental College.`,
		},
		{
			Kind: "authority", Title: "Shreya Singhal v. Union of India",
			Citation: "(2015) 5 SCC 1", Visibility: "all",
			Body: `Supreme Court of India.

Section 66A of the Information Technology Act, 2000 was struck down as
violative of Article 19(1)(a). The Court distinguished discussion and advocacy
from incitement, holding that only the latter falls within Article 19(2), and
held the provision vague and overbroad.

Significance for this problem: directly relevant to the recital of
"inflammatory content" as a ground. A restriction aimed at content that has
not crossed into incitement, and which is framed so broadly that lawful speech
is swept up, is vulnerable.`,
		},
		{
			Kind: "authority", Title: "Anuradha Bhasin v. Union of India",
			Citation: "(2020) 3 SCC 637", Visibility: "all",
			Body: `Supreme Court of India.

Challenge to the communication restrictions imposed in Jammu and Kashmir from
August 2019. The Court held that freedom of speech and expression and the
freedom to practise a profession through the medium of the internet are
constitutionally protected; that suspension orders are subject to judicial
review and must be published; that an indefinite suspension is impermissible;
and that orders must satisfy the proportionality standard and be reviewed
periodically.

Significance for this problem: the closest authority on Issues I and II. Both
sides must engage with it. The Appellant will emphasise that the Court did not
hold every suspension unconstitutional; the Respondents will emphasise
publication, periodic review and the bar on indefinite suspension.`,
		},
		{
			Kind: "authority", Title: "Internet and Mobile Association of India v. Reserve Bank of India",
			Citation: "(2020) 10 SCC 274", Visibility: "all",
			Body: `Supreme Court of India.

A circular prohibiting regulated entities from dealing in virtual currencies
was set aside on proportionality grounds. The Court examined whether the
regulator had considered less intrusive measures and whether any demonstrable
harm had been shown.

Significance for this problem: useful on the least-restrictive-alternative
limb, and on the evidentiary burden borne by the State when it asserts a risk
that has not materialised. Note the Appellant's available distinction: the
measure there was economic regulation, not public order.`,
		},
		{
			Kind: "guidance", Title: "Memorial Format Requirements", Visibility: "all",
			Body: `Every memorial must contain, in this order:

1.  Cover page stating the case title, the side represented and the team code
2.  Table of Contents
3.  Index of Authorities, separated into cases, statutes and other sources
4.  Statement of Jurisdiction
5.  Statement of Facts
6.  Issues Raised
7.  Summary of Pleadings
8.  Arguments Advanced
9.  Prayer

Limits:
- Arguments Advanced: 4,000 words
- Summary of Pleadings: 600 words
- Statement of Facts: 1,200 words
- Footnotes may not contain substantive argument

Citations follow a consistent style throughout. The team code must not appear
anywhere except the cover page, and no identifying information about the
institution may appear at all.

Structure and compliance are machine-checked before any evaluation of content.`,
		},
	}
}

func seedKnowledgeSources(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error {
	for _, src := range mootSources() {
		title := src.Title
		if src.Citation != "" {
			title = fmt.Sprintf("%s, %s", src.Title, src.Citation)
		}

		var sourceID uuid.UUID
		// assessment_id stays NULL: these are organisation-level materials a
		// teacher attaches when creating an assessment.
		if err := tx.QueryRow(ctx, `
			INSERT INTO knowledge_sources (organization_id, kind, title, visibility)
			VALUES ($1, $2, $3, $4) RETURNING id`,
			orgID, src.Kind, title, src.Visibility).Scan(&sourceID); err != nil {
			return fmt.Errorf("seed knowledge source %q: %w", src.Title, err)
		}

		body := strings.TrimSpace(src.Body) + "\n"
		sum := sha256.Sum256([]byte(body))
		storageKey := "seed/" + slug(src.Title) + ".txt"

		if _, err := tx.Exec(ctx, `
			INSERT INTO documents
				(organization_id, knowledge_source_id, storage_key, filename,
				 content_type, byte_size, sha256, parse_status, extracted_text)
			VALUES ($1,$2,$3,$4,'text/plain',$5,$6,'parsed',$7)`,
			orgID, sourceID, storageKey, slug(src.Title)+".txt",
			len(body), sum[:], body); err != nil {
			return fmt.Errorf("seed document for %q: %w", src.Title, err)
		}
	}
	return nil
}

func slug(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
