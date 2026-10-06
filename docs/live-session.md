# Live session

## Latency budget

Target: student stops speaking to judge audio starts, **1200 ms**.

| Step | Budget | Note |
|---|---|---|
| Client capture and encode | 20-40 ms | 20 ms frames, PCM16 16 kHz mono |
| Network uplink | 20-60 ms | |
| Server VAD endpoint decision | 200-300 ms | dominant fixed cost, tunable |
| STT finalise tail | 100-200 ms | partials already streaming |
| Monitor-tier decision | 120-250 ms | Qwen3-4B, short prompt, structured out |
| Coordinator arbitration | under 5 ms | deterministic code, no model |
| Question selection, bank hit | 20 ms | pgvector nearest neighbour |
| Question selection, live LLM | 700-1500 ms | fallback path only |
| TTS first chunk | 200-400 ms | Kokoro, sentence-chunked |
| Downlink and jitter buffer | 60-100 ms | |

Bank path lands near **800 ms** and feels like a judge. The live-LLM path lands
near **2000 ms** and feels like a lagging video call. The bank must serve the
majority of interruptions; the LLM is the fallback, not the hot path.

## Transport

WebSocket, not WebRTC, for V1.

- Audio up: binary frames, PCM16 16 kHz mono, 20 ms, with a 4-byte sequence
  header.
- Audio down: binary Opus or PCM chunks from TTS.
- Control down: JSON (transcript partials, judge state, timers, animation
  cues).

WebRTC buys packet-loss resilience and roughly 100-200 ms on bad networks, and
costs ICE, STUN, TURN, an SFU and permanent operational overhead. Transport
sits behind a `MediaTransport` interface so LiveKit can slot in at V3.

## Pipeline

```
mic -> client VAD (gate only, never trusted)
    -> WS binary frames
    -> server VAD (Silero, authoritative)
    -> streaming STT (faster-whisper small.en, LocalAgreement-2)
    -> partials straight back to the browser
    -> finals to the session actor (Go) and to the monitor tier
    -> assertion ledger update
    -> monitor tier bid
    -> coordinator arbitration (deterministic)
    -> session actor grants the speaking floor
    -> question: bank retrieval, else judge LLM
    -> TTS sentence-chunked streaming
    -> browser playback plus avatar state change
```

## Tiered interruption engine

**Tier 0 — deterministic, no model, free.**
Silence over 900 ms. Turn over 90 s. Stage time budget crossed. Judge idle over
N minutes. Speaker handoff reached.

**Tier 1 — monitor, Qwen3-4B, runs on every final segment.**
Short prompt: last 3 segments plus the open assertion ledger. Structured output
only:

```json
{
  "action": "interrupt | continue | note",
  "priority": 0.82,
  "reason": "unsupported legal proposition",
  "claim_ref": "a_17",
  "seed": "authority for the proportionality standard"
}
```

**Tier 2 — judge, runs only on an approved interrupt.**
Bank retrieval against `seed` plus the current claim embedding. On a miss or a
low similarity score, Qwen3-8B generates, grounded in retrieved chunks.

Roughly 15-25 judge turns per 12-minute speech. A continuous LLM loop would be
100x the cost for worse latency.

## Coordinator

Deterministic code, never a model. Judges submit bids; the coordinator applies:

1. Is the speaking floor free?
2. Has this judge's cooldown elapsed?
3. Does the current stage permit interruption?
4. Is `priority` above the profile threshold?
5. Does the judge have interruption quota left?
6. Is the candidate mid-sentence according to VAD?

The highest surviving bid wins. Everything else is dropped and logged to
`judge_actions` with a reason. Three judges never talk over each other because
the floor is a mutex, not a prompt instruction.

## Barge-in and echo

The most underestimated part of the system.

- **Headphones are mandatory**, enforced in the device check. Without them the
  judge transcribes its own voice. This is a hard product rule.
- `echoCancellation: true`, `noiseSuppression: true` on `getUserMedia`.
- Server-side gate: STT input is suppressed during TTS playback unless input
  energy clears a threshold for over 300 ms.
- On barge-in: cancel TTS immediately, flush the playback buffer, emit
  `judge_interrupted_by_candidate`, release the floor.

## Reliability

| Failure | Behaviour |
|---|---|
| Browser refresh | Reconnect with `last_seq`; server replays the event delta |
| Network blip | Client ring-buffers the last 10 s of audio and replays unacked frames by sequence |
| Mic disconnect | Session pauses, resume budget starts, moderator notified |
| STT down | Session enters `DEGRADED`; judge falls back to time-based bank questions; gap recorded in the report |
| TTS down | Question renders as on-screen text; student answers normally |
| LLM timeout over 2.5 s | Bank question used. A question is always available |
| One judge agent fails | Remaining judges continue; the report notes the missing evaluation |
| Session actor crashes | State rebuilt by replaying `session_events` |
| Postgres restart | Media plane buffers events in Redis and flushes on recovery |

## Admission control

`MAX_CONCURRENT_LIVE_SESSIONS` is a hard gate backed by `session_slots`. A 16 GB
GPU supports roughly 3-5 concurrent sessions with Whisper small, Kokoro and two
resident LLMs. Session 6 would degrade all five, so it queues instead. Students
see a waiting-room position, not a broken session.
