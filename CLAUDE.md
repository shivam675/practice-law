# MegaMoot — working notes for Claude Code

AI Live Assessment Platform. Moot court is the first assessment type, not the
architecture. Read `docs/architecture.md` before any structural change.

## Layout

```
apps/api      Go control plane: auth, RBAC, tenancy, workflow, sessions
apps/ai       Python, stateless: document extraction; grading next
apps/media    Python, GPU: VAD, STT, TTS, monitor tier            (not built yet)
apps/web      React + TypeScript + Tailwind v4
packages/contracts  protobuf, shared across all three             (not built yet)
infra         compose, proxy, deployment
docs          architecture, domain model, state machine, ADRs
```

## Commands

```bash
cp .env.example .env
docker compose up -d postgres redis minio
docker compose up api
```

Go toolchain is not installed on the dev host; build through Docker:

```bash
docker run --rm -v "//x/megamoot/apps/api":/src -w /src golang:1.23-alpine \
  sh -c "go mod tidy && go build ./... && go vet ./..."
```

## Rules that are not negotiable

1. **`organization_id` in every tenant-scoped query.** No exceptions. RLS is a
   second line, not the first.
2. **No model emits a final score.** Models produce per-criterion scores with
   evidence; the application computes totals.
3. **No AI actor causes a state transition.** Agents propose, the engine
   decides. Everything goes through one `Transition` function.
4. **Server-side deadlines only.** Client timers are decoration. Submission
   locking is a conditional UPDATE, not an application check.
5. **Go never touches audio bytes.** The browser dials the media plane
   directly with a short-lived ticket.
6. **`session_events` is append-only.** Never updated, never deleted by
   application code. It is the audit log and the replay source.
7. **Untrusted content never enters a system prompt.** A student's memorial is
   hostile input. See `docs/security.md`.
8. **Evidence quotes are verified against the source** before they reach a
   student.
9. **The web app is light mode only** and reads its colours from the tokens in
   `apps/web/src/styles/app.css`. No hard-coded hex values in components. See
   `docs/design-system.md`.
10. **The access token lives in memory only.** Never `localStorage`. The
    refresh cookie is httpOnly and the client never reads it.
11. **A provider credential is write-only.** It is sealed with AES-GCM under
    `CONFIG_ENCRYPTION_KEY` and no route returns it. Responses carry
    `api_key_hint`, the last four characters, and nothing more.
12. **Model routing is a row, not an environment variable.** `model_providers`
    and `model_bindings` are platform scoped and edited at `/admin/models`.
    A URL and a token in `.env` cannot be rotated without a deploy.

## Conventions

- Permissions, never role names. `p.Can("assessment.grade")`.
- Errors wrap with `%w` and carry context. Never swallow one.
- `httpx.Error` is the only error shape the API returns.
- SQL is hand-written and parameterised. No ORM.
- Migrations are append-only; editing an applied file is rejected at startup.
- One stage kind added means a platform decision, not a config field.

## Where the build is

Milestones 1 to 5 are real: identity, RBAC, templates, rubrics, assessments,
the workflow engine, submissions and the deterministic format checker. The web
app covers them.

**No model has been called yet.** `apps/ai` does document extraction and
nothing else; `apps/media` does not exist, so there is no STT, no TTS and no
VAD. `SessionRoom.tsx` says so at the top: it is a layout shell. The schema
(`ai_profiles`, `ai_requests`, `assertions`, `judge_actions`, `evaluations`)
is ahead of the code on purpose.

What exists of milestone 6: the provider adapter (`internal/llm`), the request
ledger, platform model configuration and AI actor profiles. The grading calls
themselves are not written.

## Hardware reality

The target GPU box is 16 GB VRAM. Resident during live sessions: Qwen3-8B,
Qwen3-4B, faster-whisper small.en, Kokoro. Roughly 11 GB. The 30B MoE grader
loads only when no live session is running.

**Concurrent live sessions: 3 to 5.** Admission control is not optional.
