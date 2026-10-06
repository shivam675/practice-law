# ADR 0005: Scores are computed in application code, never emitted by a model

**Status:** Accepted
**Date:** 2026-10-06

## Context

A model that outputs a final grade cannot be audited, cannot be recomputed
after a rubric change and cannot survive a student dispute.

## Decision

Models emit per-criterion scores with evidence spans. The application verifies
each quote against its source, enforces bounds from the rubric row, and
computes the weighted total. `prompt_version` and `model_version` are stamped
on every evaluation.

## Consequences

One model call per criterion instead of one per document: more calls, but
traceable, re-gradable one criterion at a time, and defensible.
