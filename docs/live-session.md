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
6. It decides whether to continue, record a note, or propose an intervention.
7. Code checks the style, priority, cooldown, quota, and an exact quote from the latest speech.
8. A bank question must match the speech and receive monitor approval. Otherwise, the judge generates one grounded question.
9. The media service sends the question and sentence audio. The browser acknowledges completed playback.
10. The candidate receives the speaking floor again.

Calls use the configured model bindings and timeouts. Only an approved intervention invokes question generation.
A successful decision prevents repeated evaluation of the same final segment.

## Grounding and memory

Student text and source materials reach models in tagged user messages. They do not become system instructions.
Recent conversation is bounded to 24 events and 16,000 characters. A rolling summary preserves earlier claims, authorities, concessions, and unanswered questions.
Summary events are saved as `judge_memory`. They survive reconnects but do not appear as spoken transcript turns.
Case context has a 12,000-character budget.

An intervention must quote the latest speech exactly. Generated output must contain one question of at most 30 words and an exact speech quote.
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
| Monitor or judge error | Remain silent. Show a warning. Preserve saved speech. |
| Missing parsed materials | Show a warning. Do not fabricate a case question. |
| Invalid or ungrounded question | Reject the output. Show a warning. |
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

The preparation script creates `qwen3-live` from installed weights and checks a JSON response. It refuses Thinking-only weights.
Bind monitor and judge to that model in Model settings. Keep grading on its independently selected provider.
With the API running, the existing routing script can update only the live tiers:

```powershell
./scripts/use-local-model.ps1 -Model qwen3-live -Tiers monitor,judge
```

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
