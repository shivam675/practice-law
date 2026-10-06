# AI harness

## Model tiers

Sized for one 16 GB GPU (RTX 5060 Ti / Quadro RTX 5000 class).

| Tier | Model | Residency | Latency | Used for |
|---|---|---|---|---|
| Monitor | Qwen3-4B Q5 | always in VRAM | ~150 ms | interruption bids, claim extraction |
| Judge | Qwen3-8B Q5 | always in VRAM | ~700 ms | question generation on a bank miss |
| Grader | Qwen3-30B-A3B Q4 | loaded only when no live session runs | seconds | memorial grading, session evaluation, reports |
| STT | faster-whisper small.en | always in VRAM | RTF ~0.1 | streaming transcription |
| TTS | Kokoro-82M | always in VRAM | ~250 ms to first chunk | judge voice |

VRAM: roughly 6 + 3 + 1 + 0.5 = 11 GB resident, leaving headroom. The 30B MoE
is swapped in for offline work only, because at Q4 it spills into system RAM and
produces around 25 tokens per second, which cannot meet the 1200 ms live budget.

Migration to a hosted provider is a config change: `LLM_PROVIDER` plus the model
names. Nothing in the application references Ollama.

## Harness components

```
ModelRouter       (profile, task, budget, latency_class) -> provider + model
ProviderAdapter   ollama | openai_compatible | vllm | anthropic
StructuredGate    Pydantic schema -> one repair retry -> reject
RequestLedger     tokens, latency, cost, prompt_version, session_id
```

A rejected judge decision degrades to `continue`. It never crashes a session.
Every call is ledgered to `ai_requests`, which is also the billing source.

## AI profiles

Versioned rows in Postgres, not files in the repo, so a teacher can tune a judge
without a deploy.

```yaml
name: Strict Appellate Judge
role: judge
model_tier: judge
temperature: 0.4
personality: { firmness: 0.8, interruption_frequency: 0.6, patience: 0.5 }
focus: [precedent, logical_consistency, legal_authority]
interruption_policy:
  min_priority: 0.65
  cooldown_s: 45
  max_per_stage: 8
permissions: [ask_question, interrupt, evaluate]
rag_sources: [problem, authorities, statutes]
```

`permissions` is enforced by the coordinator in code, not by the prompt.

## Question bank

Generated at two points:

1. **On assessment publish** — 40 to 80 general questions from the problem and
   the authorities, per side.
2. **On submission grading** — 20 more targeted at that team's actual
   arguments.

Each item stores `text`, `category`, `difficulty`, `targets_claim_type`,
`source_refs`, `embedding`. Retrieval at runtime is a pgvector nearest-neighbour
query against the monitor's `seed` plus the current claim embedding, filtered by
category and by already-asked. Roughly 20 ms, and the questions are better than
anything a 8B model invents under time pressure.

## Grading contract

One call per rubric criterion, not one call per document. Each returns:

```json
{
  "criterion_id": "legal_reasoning",
  "score": 16,
  "max_score": 20,
  "reasoning": "...",
  "evidence": [
    { "source_kind": "submission", "locator": "Section 3.2",
      "quote": "the proportionality standard requires..." }
  ]
}
```

Then, in application code:

1. **Quote verification.** Every quote is matched against the source text,
   exact first, then fuzzy above 0.9 similarity. No match means reject and
   retry once, then store with `verified = false` and exclude it from the
   student-facing report.
2. **Bounds check.** `0 <= score <= max_score` from the rubric row, not from
   the model.
3. **Weighted total computed in code.** No model emits a final score, ever.
4. **Version stamping.** `prompt_version` and `model_version` on the evaluation,
   so a disputed grade is reproducible and a prompt change can trigger a
   targeted re-grade.

Re-grading one disputed criterion is one call, not a full re-run.

## Format compliance checker

Deterministic, no model involved. Moot memorials are scored on structure, and
structure is a rule:

- required sections: cover page, table of contents, index of authorities,
  statement of jurisdiction, statement of facts, issues raised, summary of
  pleadings, arguments advanced, prayer
- word limits per section
- citation format
- page limits, footnote presence

Accurate, instant, free, and institutions value it. Runs before the LLM grader
and feeds the `structure` criterion directly.

## Prompt injection defence

A student's memorial is untrusted input that an LLM will read.

1. Untrusted content is always a delimited user-role block. Never the system
   prompt, never a template interpolation.
2. The model returns a schema. The application computes the outcome.
3. Score bounds are enforced against the rubric row.
4. An injection-pattern detector flags and logs rather than blocks; a legal
   document discussing instructions will trigger false positives.

## Evaluation flow

```
memorial upload
  -> extract (sandboxed parser, no network, resource capped)
  -> format compliance checker (deterministic)
  -> chunk and embed into document_chunks
  -> per-criterion grading calls, grounded by retrieval
  -> quote verification
  -> criterion_scores + evidence_spans persisted
  -> targeted question bank generation

live session ends
  -> transcript + assertion ledger + judge_actions
  -> per-criterion session evaluation
  -> report composer merges written and oral evaluations
  -> human_review stage
  -> publish transition makes the report visible
```

## Testing the AI layer

- Recorded-response fixtures for every agent, so CI never calls a model.
- Schema conformance tests on every structured output.
- Scenario tests: given a transcript containing a known contradiction, the
  monitor must emit `action = interrupt` with `reason` referencing it.
- Quote-verification tests with deliberately hallucinated quotes.
- A golden set of graded memorials, once real scored examples exist.
