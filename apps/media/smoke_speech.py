"""Real Paradee -> PCM WebSocket -> Whisper -> persisted transcript check.

Run through scripts/smoke-live.ps1 -Speech. Uses synthetic speech, no microphone.
"""
import asyncio
import json
import sys
import time
import re

import numpy as np
from paradee import Paradee
from websockets.asyncio.client import connect


async def main():
    request = json.load(sys.stdin)
    voice = Paradee()
    synthesis_started = time.monotonic()
    audio = voice(request["text"])
    print(f"TTS: {time.monotonic() - synthesis_started:.2f}s for {len(audio)/24000:.2f}s of audio", flush=True)
    audio = np.interp(np.arange(0, len(audio), 1.5), np.arange(len(audio)), audio)
    pcm = (np.clip(audio, -1, 1) * 32767).astype("<i2").tobytes()
    pcm += bytes(16000 * 2 * 2)
    async with connect("ws://localhost:8400/speech", origin="http://localhost:5173", max_size=8_000_000) as ws:
        await ws.send(json.dumps({"ticket": request["ticket"]}))
        assert json.loads(await ws.recv())["type"] == "ready", "Speech service not ready"
        floor_free = asyncio.Event()
        floor_free.set()
        async def send():
            for offset in range(0, len(pcm), 3200):
                await floor_free.wait()
                await ws.send(pcm[offset:offset + 3200])
                await asyncio.sleep(0.1)
            await ws.send(json.dumps({"type": "finish"}))
        sender = asyncio.create_task(send())
        saved = False
        transcript = []
        started = time.monotonic()
        while True:
            response = await asyncio.wait_for(ws.recv(), 120)
            if isinstance(response, bytes):
                assert response.startswith(b"RIFF"), "Expected WAV audio"
                continue
            event = json.loads(response)
            assert event["type"] != "error", event
            if event["type"] == "question":
                floor_free.clear()
            if event["type"] == "judge_audio_end":
                await ws.send(json.dumps({"type": "playback_done"}))
                floor_free.set()
            if event["type"] == "transcript_final":
                transcript.append(event["text"])
                print("PASS: Whisper transcribed Kokoro speech", flush=True)
            if event["type"] == "saved":
                saved = True
            if event["type"] == "drained":
                assert saved, "Speech was not persisted before completion"
                break
        await sender
        expected = re.findall(r"\w+", request["text"].lower())
        actual = re.findall(r"\w+", " ".join(transcript).lower())
        distance = list(range(len(actual) + 1))
        for i, word in enumerate(expected, 1):
            row = [i]
            for j, got in enumerate(actual, 1):
                row.append(min(row[-1]+1, distance[j]+1, distance[j-1]+(word != got)))
            distance = row
        error_rate = distance[-1] / max(1, len(expected))
        assert error_rate < .15, f"Word error rate {error_rate:.1%}: {' '.join(transcript)}"
        elapsed = time.monotonic() - started
        print(f"PASS: speech saved in {elapsed:.1f}s; drain overhead {elapsed-len(pcm)/32000:.1f}s; word error rate {error_rate:.1%}", flush=True)


asyncio.run(main())
