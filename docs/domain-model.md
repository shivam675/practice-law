# Domain model

## Tenancy

Every tenant-scoped table carries `organization_id NOT NULL`. Indexes lead with
it. Enforced at the repository layer, with Postgres RLS as defence in depth.
Retrofitting tenancy costs months; carrying it from day one costs days.

## Core entities

### Identity and access

- `organizations` — tenant root.
- `users` — `organization_id`, email (unique per org), argon2id hash, status.
- `roles` — per-org, `is_system` for the built-ins.
- `permissions` — global catalogue of `resource.action` strings.
- `role_permissions`, `user_roles` — many-to-many.
- `refresh_tokens` — rotating family with reuse detection.
- `audit_logs` — actor, action, target, before/after, request id.

Roles are collections of permissions. Authorisation checks a permission, never
a role name.

### Assessment definition

- `assessment_types` — `moot_court`, `viva`, `interview`. Metadata only.
- `assessment_templates` and `assessment_template_versions`.
  A version becomes immutable once an assessment references it, so templates
  can evolve while assessments are in flight. A version holds `stages` (JSONB
  stage spec list) and a `rubric_id`.
- `rubrics` and `rubric_criteria` (`weight`, `max_score`, `scope`).
  `scope` tells the evaluator which stage produces that criterion's score.
- `ai_profiles` — versioned rows in Postgres, not YAML in the repo. A teacher
  tunes interruption frequency without a deploy.

### Assessment instance

- `assessments` — a template version instantiated for a cohort, with its own
  schedule, deadlines and configuration overrides.
- `knowledge_sources`, `documents`, `document_chunks` (pgvector plus tsvector).
  The moot problem, statutes, authorities, marking guidance.
- `question_bank_items` — questions precomputed at publish time and again after
  each submission, embedded. The live hot path retrieves from here rather than
  generating.

### Participation

- `teams` — **always present**. A solo candidate is a team of one. This removes
  the individual-versus-team dual code path entirely.
- `team_members` — `user_id`, `speaking_order`, `role`
  (`speaker` or `researcher`).
- `assignments` — `(assessment_id, team_id, side)`. The unit of work, the unit
  of grading and the unit of reporting.
- `artifacts` — uploaded submissions, append-only versions. `locked_at` is set
  by a conditional UPDATE, which is the real deadline enforcement.

### Live session

- `sessions` — a scheduled instance of a `live_turn` stage group.
- `session_participants` — **one table for humans and AI**.
  `kind` is `human` or `ai`; `role` is `candidate`, `judge`, `examiner`,
  `opponent`, `observer` or `moderator`; `ai_profile_id` is nullable.
  Student-versus-student in V4 is two rows with `kind = 'human'`. No schema
  change.
- `session_events` — append-only, `seq` monotonic per session, JSONB payload.
- `transcript_segments` — projection: speaker, text, confidence, time range.
- `assertions` — **the assertion ledger**. Extracted claims with `claim_type`,
  `cited_authority`, `support_status` and `contradicts[]`. Backbone of
  interruption, contradiction detection and evaluation. Without it, "detect a
  contradiction" means re-reading the whole transcript with an LLM every few
  seconds.
- `judge_actions` — every bid, approved or dropped, with the reason.

### Results

- `evaluations` — per assignment per stage. Carries `prompt_version` and
  `model_version`. Immutable; corrections supersede rather than overwrite.
- `criterion_scores` — one row per rubric criterion. This is what the model
  produces.
- `evidence_spans` — `source_ref`, `locator`, `quote`, `verified`. A span with
  `verified = false` never reaches a student.
- `reports` — composed, versioned, published by an explicit transition.
- `ai_requests` — model, tokens in and out, latency, cost, prompt version,
  session id. Needed for billing and for re-grading after a prompt change.

### Operations

- `scheduled_transitions` — durable timers. Never `time.AfterFunc`, which dies
  with the process.
- `session_slots` — admission control. A hard ceiling on concurrent live
  sessions, because the GPU has one.

## Invariants

1. Totals are computed in application code from `criterion_scores`. No model
   ever emits a final score.
2. An evidence quote is verified against its source before storage.
3. No AI actor can cause a state transition. Agents propose; the engine decides.
4. Every deadline is checked server-side at transition time. Client timers are
   decoration.
5. `organization_id` appears in the WHERE clause of every tenant-scoped query.
