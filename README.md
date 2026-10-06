# MegaMoot — AI Live Assessment Platform

Generic, configuration-driven platform for live AI-evaluated assessments.
Moot court is the first assessment type, not the architecture.

## Planes

| Plane | Service | Language | Owns |
|---|---|---|---|
| Control | `apps/api` | Go | auth, RBAC, tenancy, workflow state machine, timers, session events, persistence |
| AI | `apps/ai` | Python | grading, report generation, judge agents, retrieval |
| Media | `apps/media` | Python (GPU) | live audio, VAD, STT, TTS, monitor tier |
| Client | `apps/web` | React + TS | student / teacher / admin UIs, live session room |

Audio never routes through Go. Go issues a short-lived media ticket; the browser
connects straight to the media plane. See `docs/live-session.md`.

## Quick start

```bash
cp .env.example .env
docker compose up -d postgres redis minio
docker compose up api
```

API health: http://localhost:8080/healthz

## Docs

| File | Contents |
|---|---|
| `docs/architecture.md` | planes, boundaries, request paths |
| `docs/domain-model.md` | entities and invariants |
| `docs/state-machine.md` | assignment / stage / session machines |
| `docs/live-session.md` | audio pipeline, latency budget, failure modes |
| `docs/ai-harness.md` | model tiers, profiles, coordinator, grading contract |
| `docs/security.md` | threat model and controls |
| `docs/deployment.md` | dev + single-GPU production topology |
| `docs/roadmap.md` | milestones |
| `docs/adr/` | architecture decision records |
