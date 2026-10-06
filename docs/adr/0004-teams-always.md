# ADR 0004: Model every assignment against a team, even a solo one

**Status:** Accepted
**Date:** 2026-10-06

## Context

Real moots run two speakers plus a researcher per side. Interviews and vivas
run one candidate. Supporting both with nullable `user_id` and `team_id`
columns produces a dual code path through grading, reporting and session
participation.

## Decision

`teams` always exist. A solo candidate is a team of one. `assignments`
references `team_id` only.

## Consequences

One code path. A slightly heavier insert when creating solo assignments.
Student-versus-student in V4 needs no schema change.
