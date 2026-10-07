"""Real Kokoro -> PCM WebSocket -> Whisper -> persisted transcript check.

Run through scripts/smoke-live.ps1 -Speech. Uses synthetic speech, no microphone.
"""
import asyncio
import json
import sys
import time

import numpy as np
from kokoro import KPipeline
from websockets.asyncio.client import connect


async def main():
    request = json.load(sys.stdin)
    voice = KPipeline(lang_code="a", device="cpu")
    audio = np.concatenate([a.numpy() for _, _, a in voice(request["text"], voice="af_heart") if a is not None])
    audio = np.interp(np.arange(0, len(audio), 1.5), np.arange(len(audio)), audio)
    pcm = (np.clip(audio, -1, 1) * 32767).astype("<i2").tobytes()
    pcm += bytes(16000 * 2 * 2)
    async with connect("ws://localhost:8200/speech", origin="http://localhost:5173", max_size=8_000_000) as ws:
        await ws.send(json.dumps({"ticket": request["ticket"]}))
        assert json.loads(await ws.recv())["type"] == "ready", "Speech service not ready"
        async def send():
            for offset in range(0, len(pcm), 3200):
                await ws.send(pcm[offset:offset + 3200])
                await asyncio.sleep(0.1)
            await ws.send(json.dumps({"type": "finish"}))
        sender = asyncio.create_task(send())
        saved = False
        started = time.monotonic()
        while True:
            response = await asyncio.wait_for(ws.recv(), 120)
            if isinstance(response, bytes):
                assert response.startswith(b"RIFF"), "Expected WAV audio"
                continue
            event = json.loads(response)
            assert event["type"] != "error", event
            if event["type"] == "transcript_final":
                assert len(event["text"].split()) > 8, event
                print("PASS: Whisper transcribed Kokoro speech", flush=True)
            if event["type"] == "saved":
                saved = True
            if event["type"] == "drained":
                assert saved, "Speech was not persisted before completion"
                break
        await sender
        print(f"PASS: speech saved and drained in {time.monotonic() - started:.1f}s", flush=True)


asyncio.run(main())
