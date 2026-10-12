# Live session

## Judging style

Choose a style for each live stage in the template editor. Assessments use the selected template version.
Existing stages without `judging_style` use `balanced`.

| Style | During speech | At a natural pause |
|---|---|---|
| Balanced | Intervene for sustained topic drift or a clear contradiction, with priority at least 0.85 | Address a material weakness or unanswered question |
| Strict | Also challenge unsupported or unclear material claims, with priority at least 0.85 | Address a material weakness or unanswered question |
| Patient | Wait | Address a material weakness or unanswered question |

The interruption setting remains independent. `disabled` prevents all questions. `limited` permits at most two questions per session.
Profile cooldowns, quotas, and minimum priorities still apply. Eligible presiding judges receive first priority.

## Pipeline

1. The browser sends PCM16 audio at 16 kHz over a WebSocket.
2. Server VAD detects speech and pauses. Whisper produces partial text and final segments.
3. A natural pause ends the turn. Continuous speech splits after eight seconds at a recognized word boundary.
4. Final segments are saved before the judge runs. Continuous segments use `speaking=true`.
5. The monitor reads case materials, the candidate memorial, recent conversation, and a rolling summary.
6. It first classifies the speech as `argument`, `answer`, `procedural` (about the hearing or the app) or `off_topic`. Then it decides whether to continue, record a note, or propose an intervention.
7. Code makes the reason agree with the classification, then checks the style, priority, cooldown, quota, and a quote from the latest speech. A contradiction also needs the earlier conflicting words on the record.
8. The judge writes one turn. Up to three prepared bank questions are offered to it, never to the monitor. Code then frames the turn: see Bench behaviour.
9. The media service sends the question and sentence audio. The browser acknowledges completed playback.
10. The candidate receives the speaking floor again.

Calls use the configured model bindings and timeouts. Only an approved intervention invokes question generation.
A successful decision prevents repeated evaluation of the same final segment.

## Bench behaviour

The bench behaves like a moot judge, and the parts a small model does not do reliably are done in code.

| Counsel | Bench |
|---|---|
| Argues the case, even weakly | Lets counsel continue |
| Makes a material unsupported claim, an unclear point, or a contradiction | Asks one question about it |
| Talks about the app, the screen, audio, or asks for help | At a pause: "Noted, Counsel." then returns counsel to a named case issue. Never treated as a legal claim |
| Drifts off the case | Redirects to a named case issue |
| Dodges a bench question | Presses once: "Counsel, the bench asks again: ..." |
| Dodges again | Moves on. A question is never asked a third time |

A question is a repeat when most of the shorter question's content words appear in an earlier one, so rewording does not get past the check.

## Grounding and memory

Student text and source materials reach models in tagged user messages. They do not become system instructions.
Recent conversation is bounded to 12 events and 6,000 characters. A rolling summary preserves earlier claims, authorities, concessions, and unanswered questions.
Summary events are saved as `judge_memory`. They survive reconnects but do not appear as spoken transcript turns.
Case context has a 6,000-character budget. Small models read short context better than long.

An intervention must quote the latest speech. Matching ignores case and punctuation, because speech recognition punctuates by guesswork. Generated output must be one question of at most 30 words.
These checks reject fabricated quotes and malformed output. They do not prove the model's legal interpretation is correct.

Speech can save during inference. A newer final segment causes the old intervention to be discarded.
Approved and dropped decisions are recorded in `judge_actions` when a profile exists. Model failures enter `ai_requests` and API logs.

## Audio handoff

The `question` event gives the judge the speaking floor before synthesis starts.
Browser and server microphone frames become silence during playback. This prevents judge audio from becoming candidate speech.
`judge_audio_end` marks completed synthesis. The browser waits for its audio queue to finish before sending `playback_done`.
The server releases a missing acknowledgement after 30 seconds of incoming audio.
Students wait for the judge to finish. Headphones and browser echo cancellation remain recommended.

## Failure behaviour

| Failure | Behaviour |
|---|---|
| Model provider unreachable or timed out | Remain silent. Show a judge warning. Preserve saved speech. |
| Missing parsed materials | Show a warning. Do not fabricate a case question. |
| Invalid, ungrounded or repeated question | Reject the output and stay silent. Log it; the student sees nothing. |
| Newer speech arrives during a decision | Discard the old decision silently. |
| Judge recovers | The next successful decision clears the judge warning. Microphone and connection errors stay. |
| TTS failure or blocked playback | Keep the question on screen. Release the floor through playback completion handling. |
| Reconnect | Reuse the session and read its saved transcript and memory. |
| Slow connection or failed transcription | Preserve saved turns. Show an error and allow reconnection. |

Live model failures do not trigger generic fallback questions.

## Local model setup

Use a non-thinking model for live tiers. The tag `qwen3:4b` can contain Thinking-only weights.
Check `/api/show`. Thinking-only weights cannot satisfy short deadlines by setting `reasoning_effort=none`.

```powershell
ollama pull qwen3:4b-instruct-2507-q4_K_M
./scripts/prepare-live-model.ps1
```

The preparation script creates `qwen3-live` from installed weights with a 16K context cache and checks a JSON response. It refuses Thinking-only weights.
Bind monitor and judge to that model in Model settings. Grading uses its own binding; locally that is Qwen3 8B, prepared the same way:

```powershell
ollama pull qwen3:8b
./scripts/prepare-live-model.ps1 -SourceModel qwen3:8b -Model qwen3-8b-live
./scripts/use-local-model.ps1 -Model qwen3-8b-live -Tiers grader
```

Keep the `embedding` tier on an embedding model such as `bge-m3` (1024 dimensions). A chat model there makes every retrieval fall back to text search.
With the API running, the existing routing script can update only the live tiers:

```powershell
./scripts/use-local-model.ps1 -Model qwen3-live -Tiers monitor,judge
```

This preserves other tiers and gives each local live call at least six seconds to finish. It disables reasoning for the selected live tiers.
Measure warm monitor, generation, and speech latency before a student pilot. A successful build does not establish conversational quality.

## Verification

```powershell
# From apps/api, against an isolated database named judge_test:
$env:JUDGE_TEST_DATABASE_URL='postgres://postgres:judge_test_only@localhost:55433/judge_test?sslmode=disable'
go test ./internal/sessions -run TestLiveJudgePipeline -count=1 -v

# Also evaluate the installed local model through the same database-backed path:
$env:JUDGE_TEST_MODEL_URL='http://localhost:11434/v1'
$env:JUDGE_TEST_MODEL='qwen3-live'
go test ./internal/sessions -run TestLiveJudgePipeline -count=1 -v

# From the repository root:
docker compose run --rm --no-deps media python -m unittest test_speech -v
```

Database tests cover grounded redirection, continuation, style differences, fabricated quotes, provider failures, malformed questions, and stale decisions.
Speech tests cover continuous monitoring, final-word preservation, audio handoff, and suppression of judge audio.

### Local model check, 10 October 2026

Tested `qwen3:4b-instruct-2507-q4_K_M` through the real database-backed judge path as `qwen3-live` on a Quadro RTX 5000 Max-Q (16 GB). The five cases passed twice, including with a 16K context cache and six-second per-call deadlines.

| Case | Result | Warm pipeline time |
|---|---|---|
| Relevant contract argument | Continue | 2.61 s |
| Sustained holiday discussion, balanced | Redirect to contract issue | 4.01 s |
| Same drift while speaking, patient | Wait | 2.36 s |
| Same drift at a pause, patient | Redirect to contract issue | 3.63 s |
| Satisfactory answer to previous question | Continue | 2.29 s |

Example: “Counsel, your holiday activities seem enjoyable, but how do they relate to the contract clause requiring written notice before termination?”

The alias's measured GPU memory allocation fell from about 12.7 GB at 64K context to 5.1 GB at 16K. These times exclude transcription and speech playback and use a short case fixture. Real microphone use, long case materials, and a coach-led moot pilot remain unverified. The application API was stopped during this check; its saved live bindings were not changed.

### Judge and grader check, 11 October 2026

Same database-backed path, 16K context aliases, temperature 0. Per-case judge time includes the monitor and the question call.

| Case | `qwen3-live` (4B instruct) | `qwen3-8b-live` |
|---|---|---|
| Relevant argument | Continue, 2.3 s | Continue, 3.5 s |
| Holiday drift, balanced, speaking | Redirect to clause 4, 2.5 s | Missed |
| Drift, patient, speaking / at pause | Wait / redirect | Wait / timed out once, missed once |
| Answered question | Continue | Continue |
| App complaint from the session transcript | "Noted, Counsel." then clause 4, 2.4 s | Same, 5.6 s |
| Still on the app after a redirect | Presses once: "Counsel, the bench asks again: ..." | Invalid reply |
| Reversal of an earlier factual claim | Missed | Missed |

The 4B instruct model stays on the live tiers. Neither model catches a contradiction across turns; the test reports it as skipped rather than passing.

Grading the 55,699-character record that had failed 112 automatic attempts:

| Grader | All 7 criteria | Evidence quotes verified |
|---|---|---|
| `qwen3-live` | 57 s | 14 of 22 |
| `qwen3-8b-live` | 48 s | 17 of 17 |

Through the application, Autograde on that assignment completed in 60 s with 7 scores and no caveats, and model calls stopped when it finished.
Real microphone use and a coach-led pilot remain unverified.
