# State machine

Three machines, not one. Collapsing them is why assessment platforms rot.

## 1. Assignment (long-lived, days to weeks)

```
ASSIGNED -> IN_PROGRESS -> AWAITING_REVIEW -> FINALIZED
   |            |
   |            +-> ABANDONED (deadline passed, nothing submitted)
   +-> WITHDRAWN                ABANDONED -> IN_PROGRESS (teacher excuses)
```

`IN_PROGRESS` holds a pointer to the current stage. It self-loops as stages
advance.

## 2. Stage (generic, applies to every stage kind)

```
PENDING -> ACTIVE -> COMPLETED
   |          |
   |          +-> GRACE -> COMPLETED      (late artifact accepted)
   |          |       +-> EXPIRED
   |          +-> FAILED                  (unrecoverable)
   +-> SKIPPED                            (conditional guard false)

EXPIRED -> COMPLETED                      (teacher override, audited)
```

Entry and exit conditions come from the stage spec. The engine knows the five
kinds; it does not know what a rebuttal is.

## 3. Live session

```
SCHEDULED -> LOBBY -> DEVICE_CHECK -> RUNNING -> ENDED -> EVALUATING -> EVALUATED
                                        |  ^
                                PAUSED <-+  |
                                   |        |
                                   +--------+  (resume within budget)
                                   +-> ABORTED (resume budget exhausted)

RUNNING <-> DEGRADED   (an AI component failed; session continues)
DEGRADED -> ENDED      (ends with gaps recorded in the report)
ABORTED  -> EVALUATING (partial evaluation)
```

`DEGRADED` is a first-class state, not an error path. A failing judge agent
must not abort a graded session.

## Rules

1. All transitions go through one function:
   `Transition(ctx, subject, from, to, cause, actor)`.
   Legal pairs live in a table, not in scattered `if` statements.
2. Every transition appends an event. The event log is the audit log.
3. Timed transitions are rows in `scheduled_transitions`, scanned by a worker.
   Never an in-process timer.
4. Transitions are idempotent on `(subject_id, from, to, cause_key)`.
5. No AI actor may call `Transition`. Agents emit proposals; the engine
   validates against the stage spec and executes.
6. Overrides by a human are transitions too, carrying actor and reason.

## Moot court stage spec

```yaml
stages:
  - id: preparation
    kind: wait
    config: { visible_resources: [problem, authorities] }

  - id: memorial
    kind: artifact_submission
    config:
      formats: [pdf, docx]
      max_bytes: 26214400
      lock_on_submit: true
      format_rules: moot_memorial_v1

  - id: memorial_eval
    kind: automated_evaluation
    config: { rubric_scope: [legal_reasoning, authorities, structure] }

  - id: oral_speaker_1
    kind: live_turn
    config: { duration_s: 720, floor: candidate, speaker_order: 1,
              interruptions: enabled }

  - id: oral_speaker_2
    kind: live_turn
    config: { duration_s: 720, floor: candidate, speaker_order: 2,
              interruptions: enabled }

  - id: rebuttal
    kind: live_turn
    config: { duration_s: 180, interruptions: limited }

  - id: oral_eval
    kind: automated_evaluation
    config: { rubric_scope: [advocacy, judge_responses, time_management] }

  - id: moderation
    kind: human_review
    config: { required: true, overrides: allowed }
```

`duration_s` is per-speaker and customisable; 720 is the default (12 minutes).
`moderation` is required because AI scores are advisory until a teacher signs
off.

## Medical viva, same engine

```yaml
stages:
  - { id: case_review,  kind: wait }
  - { id: diagnosis,    kind: live_turn, config: { duration_s: 300 } }
  - { id: questioning,  kind: live_turn, config: { duration_s: 600,
                                                   interruptions: enabled } }
  - { id: treatment,    kind: live_turn, config: { duration_s: 300 } }
  - { id: evaluation,   kind: automated_evaluation }
  - { id: sign_off,     kind: human_review, config: { required: true } }
```

No new code. That is the test for genericity.
