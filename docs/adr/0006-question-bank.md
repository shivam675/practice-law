# ADR 0006: Precompute judge questions and retrieve at runtime

**Status:** Accepted
**Date:** 2026-10-06

## Context

Generating a judge question live on an 8B model costs 700-1500 ms, which
consumes most of the latency budget and produces weaker questions than an
unhurried larger model would.

## Decision

Generate 40-80 questions per side at assessment publish time, plus 20 targeted
questions after each submission is graded. Embed them. The live path retrieves
by nearest neighbour in roughly 20 ms, filtered by category and by already-
asked. Live generation is the fallback on a miss.

## Consequences

Judge replies land near 800 ms instead of 2000 ms, question quality improves,
and GPU load during live sessions drops sharply. The cost is a generation step
at publish time and a staleness risk if the problem changes after publish.
