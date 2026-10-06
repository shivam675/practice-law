# ADR 0001: Split the system into control, AI and media planes

**Status:** Accepted
**Date:** 2026-10-06

## Context

A single backend would couple GPU capacity to ordinary web traffic. Live audio,
model inference and CRUD scale on unrelated axes.

## Decision

Three deployables from one repository. `apps/api` (Go) owns state, auth,
tenancy and the workflow engine. `apps/ai` (Python) is stateless and owns
grading, reports and judge agents. `apps/media` (Python, GPU-pinned) owns live
audio, VAD, STT, TTS and the monitor tier.

## Consequences

Audio never passes through Go, so API restarts do not kill live sessions. The
cost is one extra network boundary and a protobuf contract to maintain. A
monolith was rejected because of GPU coupling; more services were rejected as
premature.
