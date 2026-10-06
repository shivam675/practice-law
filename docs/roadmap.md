# Roadmap

## Pilot constraint

Pilot in one month. The full V1 scope does not fit one month. What fits is a
**narrow vertical slice that works end to end**, which is also what a pilot
actually needs. Breadth is what gets cut, not depth.

### In for the pilot

One organisation, seeded. One moot template. One AI judge. Two speakers per
side. Memorial upload, deterministic format check, AI grading with verified
evidence. Live oral round with streaming STT, bank-driven interruptions, Kokoro
voice, a 2D avatar with five states. Transcript. Consolidated report. Teacher
review and override. Three to four concurrent sessions.

### Out for the pilot

Self-serve assessment builder. Multiple templates in the UI. Three-judge
panels. SSO and LTI. Analytics dashboards. Student-versus-student. Video.
Anti-cheating. Billing.

## Milestones

| # | Milestone | Deliverable | Status |
|---|---|---|---|
| 1 | Foundation | Compose stack, migrations runner, config, logging, health | **done** |
| 2 | Identity | Orgs, users, argon2id, rotating refresh tokens, permissions, RBAC middleware, audit log | **done** |
| 3 | Templates and rubrics | Template versions, stage specs, rubric engine, seeded moot template | **done** |
| 4 | Assessments and assignments | Teams, sides, scheduling, access windows, workflow engine, durable scheduled transitions | **done** |
| 5 | Submissions | Blob storage adapter, upload, sandboxed parsing, format compliance checker, submission locking | **done** |
| 6 | AI plane | Harness, provider adapters, structured gate, request ledger, retrieval, per-criterion grading, quote verification | after the UI |
| 7 | Live audio | Media ticket, audio WebSocket, VAD, reconnect and replay, device check, session actor, event log | |
| 8 | Streaming STT | faster-whisper streaming, partials, endpointing, transcript projection, assertion ledger | |
| 9 | Judge | Question bank generation, monitor tier, coordinator, speaking floor, barge-in | |
| 10 | Voice and avatar | Kokoro streaming, sentence chunking, Rive avatar states, animation cues | |
| 11 | Evaluation | Session evaluation, consolidation, scoring in code, degraded-session handling | |
| 12 | Reports | Report composer, student view, evidence display, publish transition | |
| 13 | Teacher tools | Monitoring, review queue, override with reason, report annotation | |

The web application is being brought forward: screens for milestones 1 to 5
exist to build against, and early feedback on the live-session layout is worth
more than another backend milestone.
| 14 | Hardening | Tenant isolation tests, load test at 5 sessions, failure injection, retention jobs | |
| 15 | Deploy | Caddy, GPU box provisioning, backups and restore drill, runbook | |

Milestones 1 to 5 are control-plane work and carry no AI risk. Milestones 7 to
10 are the risky ones; start the audio spike in parallel with milestone 3 rather
than waiting.

## After the pilot

- **V2** — assessment builder, multiple templates, SSO and LTI 1.3, analytics,
  org onboarding.
- **V3** — three-judge panels with the coordinator already in place, richer
  avatars, multiple voices, hosted model provider.
- **V4** — student versus student (two `session_participants` rows with
  `kind = 'human'`; no schema change).
- **V5** — interviews, debates, medical viva, technical interviews. All of them
  are stage specs, not code.
