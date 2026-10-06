# Architecture

## Principle

Genericity comes from a **small closed set of stage kinds**, not from unlimited
configurability. Five kinds cover moot court, medical viva, job interview,
debate and oral exam:

| Stage kind | Meaning |
|---|---|
| `wait` | time-gated; resources become visible, nothing is collected |
| `artifact_submission` | participant uploads a document before a deadline |
| `live_turn` | real-time spoken exchange with AI or human actors |
| `automated_evaluation` | offline AI scoring against a rubric subset |
| `human_review` | a human must sign off before results publish |

Adding a sixth kind is a deliberate platform decision, not a config field.
An assessment type is a stage list plus a rubric plus AI profiles. No code.

## Planes

```
                         browser
                       /         \
            control WS /           \ audio WS (binary)
                     /               \
            +---------------+   +------------------+
            |  apps/api     |   |  apps/media      |
            |  Go           |   |  Python / GPU    |
            |  control      |<--|  VAD STT TTS     |
            |  plane        |   |  monitor tier    |
            +-------+-------+   +--------+---------+
                    | gRPC               | gRPC
                    v                    v
            +-----------------------------------+
            |  apps/ai   Python   stateless     |
            |  judge agents, grading, reports   |
            +-----------------------------------+
                    |
      +-------------+--------------+-------------+
      v             v              v             v
  Postgres       Redis           MinIO        Ollama
  +pgvector   presence/fanout    blobs         LLM
```

### Why three planes

They scale on unrelated axes. Control plane scales with registered users
(cheap). Media plane scales with concurrent live sessions and is GPU-bound
(expensive). AI plane is stateless and batchable. Merging them means buying GPU
capacity to serve PDF parsing.

## Boundaries

- **Go never touches audio bytes.** It mints a media ticket; the browser dials
  the media plane directly. Removes a hop and makes API restarts survivable
  mid-session.
- **The AI plane never writes business state.** It returns structured
  proposals. Go validates and persists.
- **The media plane never decides workflow.** It emits events and bids.
- **Only the session actor mutates a live session.** One owner process per
  session, registered in Redis. All writes serialised through it.

## Event sourcing (scoped)

`session_events` is append-only with a monotonic per-session `seq`. It is the
audit log, the replay source, the reconnect delta and the analytics source.
`transcript_segments` and `assertions` are projections of it. A crashed session
actor rebuilds from the log.

Event sourcing applies to **live sessions only**. Everything else (users,
templates, assignments) is ordinary mutable state with an audit trail.

## Contracts

Protobuf in `packages/contracts/proto`, linted and breaking-change-checked by
Buf. Generates Go, Python and TypeScript from one source.

- Go to Python: gRPC (unary and server-streaming).
- Browser to Go: REST (JSON) plus WebSocket (JSON, event types from the same
  protos).
- Browser to media: WebSocket (binary audio up, binary audio plus JSON control
  down).

## Non-goals for V1

Kubernetes, WebRTC, microservices beyond these three, a dedicated vector
database, multi-region, video recording, 3D avatars.
