# ADR 0008: The judge coordinator is code, not a model

**Status:** Accepted
**Date:** 2026-10-06

## Context

Multiple judge agents deciding independently when to speak produces agents
talking over each other and over the candidate. Prompting them to take turns is
not an enforcement mechanism.

## Decision

Agents submit structured bids. A deterministic coordinator applies floor
availability, per-judge cooldown, stage policy, priority threshold and per-
stage quota, then grants a speaking-floor mutex to exactly one bid. Every
dropped bid is logged to `judge_actions` with a reason.

## Consequences

Turn-taking is a structural guarantee rather than a prompt hope. Arbitration
costs under 5 ms. Scaling from one judge to three needs no new mechanism.
