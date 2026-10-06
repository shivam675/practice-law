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
docker compose up -d postgres redis
docker compose up -d --build api
powershell -ExecutionPolicy Bypass -File ./scripts/smoke.ps1
```

| What | Where |
|---|---|
| API | http://localhost:8080 |
| Health / readiness | `/healthz`, `/readyz` |
| Postgres (from host) | `localhost:55432` |
| Redis | `localhost:6379` |

Port 5432 falls inside the Windows reserved range on some hosts, so Postgres is
published on 55432. Inside the Compose network it is still 5432.

## Working on the Go API

No Go toolchain on the host is required; a `go` service runs the real one.

```bash
docker compose run --rm go build ./...
docker compose run --rm go vet ./...
docker compose run --rm go test ./...
docker compose run --rm go fmt ./...
docker compose run --rm go mod tidy
```

After a source change, restart the API:

```bash
docker compose up -d --build api
```

## Object storage

Development writes blobs to `./data/blobs` through the filesystem adapter
(`BLOB_DRIVER=filesystem`). Production uses any S3-compatible endpoint
(`BLOB_DRIVER=s3`). MinIO is not used: its images now sit behind registry
authentication, and a local directory is a smaller dependency than a container.

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
