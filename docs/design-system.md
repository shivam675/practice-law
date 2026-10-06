# Design system

The tokens live in `apps/web/src/styles/app.css` and are the only source of
truth. This file explains the decisions behind them so they are followed
rather than guessed at.

## Direction

Institutional assessment software for law students and faculty, in a document
register. The subject matter is publication-form legal writing (memorials, law
reports, judgments), so the interface borrows from print: warm paper, a serif
for display, generous measure, hairlines instead of boxes.

What it is not: a marketing site. There is no scroll theatre, no asymmetric
hero, no pinned sections. A student uploading a memorial twenty minutes before
a deadline is served by predictability.

## Theme

**Light only.** This runs in exam conditions on institutional machines. A
half-tested dark theme during a graded session is a liability, not a feature.
`color-scheme: light` is declared so form controls match.

When dark mode is added it will be a token swap, not a redesign: every colour
is already a variable.

## Colour

| Token | Value | Use |
|---|---|---|
| `--color-paper` | `#f8f7f4` | page background |
| `--color-paper-raised` | `#ffffff` | surfaces that are separate objects |
| `--color-paper-sunken` | `#f1efe9` | table headers, hover, inset panels |
| `--color-rule` | `#e2dfd7` | hairline dividers and surface borders |
| `--color-rule-strong` | `#cfcbc0` | input borders, secondary buttons |
| `--color-ink` | `#23221f` | body text |
| `--color-ink-muted` | `#5e5c55` | secondary text |
| `--color-ink-faint` | `#8b887f` | labels, placeholders |
| `--color-accent` | `#1e3a5f` | the single accent |

**One accent, navy ink.** Navy is what courts and academic publishing actually
signify with, it is not the AI-default violet-blue glow, and it leaves the
semantic colours room to read as state rather than as decoration.

Deliberately not used: brass, ochre, clay, oxblood. That warm-craft palette is
the default reach for anything described as "warm paper", and it makes every
product using it look like the same artisanal cookware brand.

**Semantic colours are reserved for state.** `--color-pass`, `--color-warn`
and `--color-fail` appear on stage status, lateness and compliance findings.
They are never used to make something look interesting.

## Type

| Role | Family | Why |
|---|---|---|
| Display | Newsreader Variable | Editorial serif with a real italic. Used for `h1`-`h3`, the problem text and the question in the session room |
| Interface | Outfit Variable | Labels, body, controls. Legible at 13px, which is where most of this UI lives |
| Numeric | JetBrains Mono Variable | Every number that can change |

Serif is a deliberate exception to the usual advice against it. The
justification is specific: the product's subject is publication-form legal
writing, and the reading pane shows court documents. Outside display and
document text, everything is sans.

**Numbers use `.numeric`**, which is mono with `tabular-nums`. Without it a
running session clock visibly jitters as digit widths change, and columns of
scores fail to line up.

## Shape and elevation

One radius scale: `4px` for chips and badges, `8px` for inputs and buttons,
`12px` for surfaces. No pill buttons, no sharp corners mixed in.

`Surface` is the only container primitive, and elevation means "this is a
separate object": a submission, a stage, a team. Everything else groups with
`border-rule` or with space. Cards inside cards are the fastest way to make a
product look generic.

## Motion

Near none, by intent. Transitions are 150ms on colour and a 1px press on
`:active`. Spinners and skeletons are the only moving things, and the skeleton
matches the shape of what replaces it so nothing jumps.

Everything collapses under `prefers-reduced-motion: reduce`.

## Writing

- Sentence case everywhere. No shouty uppercase labels above every heading.
- No em-dashes. A period, a comma, or a colon instead.
- Error text says what to do next, not what went wrong internally. The upload
  form maps each API error code to the next action, so "unreadable_document"
  becomes advice about scanned PDFs.
- Deadlines in the past say "2 hours ago", never a count-up next to an upload
  button that no longer works.

## Accessibility floor

- Every text and background pairing clears WCAG AA. Navy on paper and white on
  navy both clear 4.5:1.
- Labels above inputs, helper text in the markup, error text below and wired
  with `aria-describedby`. Placeholders are never labels.
- `:focus-visible` is a 2px accent ring with offset, never removed.
- Tables carry a `<caption>` and `scope` on headers.
- Icon-only buttons carry an `aria-label`.

## Icons

Phosphor only, one family, `size` in px and default weight. No hand-drawn SVG
paths.

## Session room

Three decisions worth arguing with, recorded here because they will look like
omissions otherwise:

1. **No student self-view.** Watching your own face while arguing is a
   distraction, and a level meter does the one useful thing a self-view did,
   which is confirm you are being heard.
2. **The question stays on screen until it is answered.** Under pressure people
   forget the question within ten seconds. This matters more to the experience
   than the avatar does.
3. **The clock is numerals, not a draining bar.** A bar emptying makes people
   rush. It stays quiet until the final minute.
