# MegaMoot — AI Live Assessment Platform

Generic, configuration-driven platform for live AI-evaluated assessments.
Moot court is the first assessment type, not the architecture.

## Planes

| Plane | Service | Language | Owns |
|---|---|---|---|
| Control | `apps/api` | Go | auth, RBAC, tenancy, workflow state machine, timers, session events, persistence |
| AI | `apps/ai` | Python | grading, report generation, judge agents, retrieval |
| Media | `apps/media` | Python (GPU) | live audio, VAD, STT, TTS, monitor tier |
| Client | `apps/web` | React + TS | student / teacher UIs, live session room |

Audio never routes through Go. Go issues a short-lived media ticket; the browser
connects straight to the media plane. See `docs/live-session.md`.

## Quick start

```bash
cp .env.example .env
npm run dev
```

That is the whole loop. `npm run dev` brings up Postgres, Redis and the API in
the foreground and streams logs; saving a Go file rebuilds and restarts the
server in about a second. Ctrl+C stops everything.

In a second terminal:

```bash
npm run smoke
```

| What | Where |
|---|---|
| Web | http://localhost:5173 |
| API | http://localhost:8080 |
| Health / readiness | `/healthz`, `/readyz` |
| Postgres (from host) | `localhost:55432` |
| Redis | `localhost:6379` |

Port 5432 falls inside the Windows reserved range on some hosts, so Postgres is
published on 55432. Inside the Compose network it is still 5432.

## Demo accounts

Seeded on first start, development only. `SEED_DEMO_PASSWORD` is refused in
production and `SEED_SUPERADMIN_PASSWORD` must be at least 16 characters there.

| Level | Sign in with | Password |
|---|---|---|
| Super administrator | `suadmin` | `admin123` |
| Organisation administrator | `admin@demo.test` | `ChangeMe123!` |
| Teacher | `teacher@demo.test` | `student123` |
| Student, speaker 1 | `student1@demo.test` | `student123` |
| Student, speaker 2 | `student2@demo.test` | `student123` |

The two students share `Demo Team`, which matches the moot template's
two-speaker shape and can be assigned without any further setup.

## Scripts

No Go toolchain on the host is required; a container runs the real one.

| Command | What it does |
|---|---|
| `npm run dev` | Full stack in the foreground, hot reload, Ctrl+C to stop |
| `npm run up` | Same, detached |
| `npm run logs` | Follow API logs |
| `npm run smoke` | End-to-end auth and permission checks |
| `npm run check` | fmt, vet, tests and typecheck |
| `npm test` | Go and Python tests |
| `npm run typecheck` | Web TypeScript |
| `npm run build` | Compile |
| `npm run tidy` | Resolve dependencies |
| `npm run psql` | Postgres shell |
| `npm run migrate:status` | Applied migrations |
| `npm run restart` | Restart the API container |
| `npm run stop` | Stop everything, keep data |
| `npm run reset` | Stop everything and drop volumes |

The image is only rebuilt when `go.mod` changes. Everything else is picked up
by the file watcher, which polls because Windows bind mounts do not propagate
inotify events.

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
| `docs/design-system.md` | tokens, type, colour and the decisions behind them |
| `docs/roadmap.md` | milestones |
| `docs/adr/` | architecture decision records |
