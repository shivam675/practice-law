"""Local speech plane. The control API owns sessions and persisted state."""
import asyncio
import contextlib
import io
import json
import logging
import os
import re
import time
import uuid
from datetime import datetime
from contextlib import asynccontextmanager
from functools import lru_cache

import httpx
import jwt
import numpy as np
import soundfile as sf
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from faster_whisper import WhisperModel
from faster_whisper.vad import VadOptions, get_speech_timestamps
from paradee import Paradee, SAMPLE_RATE

log = logging.getLogger("media")
RATE = 16000
TOKEN = os.environ["MEDIA_SERVICE_TOKEN"]
API = os.environ.get("CONTROL_API_URL", "http://api:8080/api/v1")
ORIGINS = os.environ.get("MEDIA_ORIGINS", "http://localhost:5173").split(",")
CAPACITY = int(os.environ.get("MAX_CONCURRENT_LIVE_SESSIONS", "3"))
# Endpointing: a long pause ends a turn, a short one does not interrupt a thinker.
END_SILENCE = float(os.environ.get("STT_END_SILENCE_SECONDS", "1.2"))
# How often the text on screen is refreshed while someone is still talking.
PARTIAL_INTERVAL = float(os.environ.get("STT_PARTIAL_INTERVAL_SECONDS", "0.6"))
# Words older than this inside one turn stop being re-decoded. Bounds every pass.
SETTLE_AFTER = float(os.environ.get("STT_SETTLE_AFTER_SECONDS", "8"))
MAX_TURN = float(os.environ.get("STT_MAX_TURN_SECONDS", "8"))
stt_lock = asyncio.Lock()
tts_lock = asyncio.Lock()
connections = set()
whisper = None
voice = None
load_error = None


def load_models():
    global whisper, voice, load_error
    try:
        device = os.environ.get("STT_DEVICE", "cpu")
        whisper = WhisperModel(os.environ.get("STT_MODEL", "small.en"), device=device,
                               cpu_threads=int(os.environ.get("STT_CPU_THREADS", "4")),
                               compute_type="float16" if device == "cuda" else "int8")
        voice = Paradee(threads=int(os.environ.get("TTS_CPU_THREADS", "2")))
        # Warm both graphs: misaki's G2P and whisper's encoder both pay a large
        # first call. Doing it here means the first judge question is not the one
        # that waits for it.
        voice("Ready.")
        whisper.transcribe(np.zeros(RATE, dtype=np.float32), language="en", beam_size=1)
    except Exception:
        load_error = "Speech models could not load. Check the media service logs."
        log.exception("load speech models")


@asynccontextmanager
async def lifespan(app):
    task = asyncio.create_task(asyncio.to_thread(load_models))
    yield
    await task


app = FastAPI(lifespan=lifespan)


@app.get("/healthz")
def health():
    return {"status": "ok"}


@app.get("/readyz")
def ready():
    from fastapi.responses import JSONResponse
    available = whisper is not None and voice is not None
    return JSONResponse({"ready": available, "stt": "faster-whisper", "tts": "paradee", "error": load_error}, status_code=200 if available else 503)


def transcribe(audio, beam=int(os.environ.get("STT_BEAM_SIZE", "3"))):
    started = time.monotonic()
    # Endpointing already applied VAD. A second pass can remove quiet words.
    segments, _ = whisper.transcribe(audio, language="en", beam_size=beam,
                                    condition_on_previous_text=False, vad_filter=False,
                                    initial_prompt=os.environ.get("STT_GLOSSARY") or None)
    text = " ".join(segment.text.strip() for segment in segments).strip()
    log.info("stt audio_s=%.2f inference_s=%.2f", len(audio) / RATE, time.monotonic() - started)
    return text


def transcribe_fast(audio):
    """Greedy decode. Partial text is replaced on screen seconds later, so the
    beam buys nothing a listener will ever see and costs them the wait."""
    return transcribe(audio, beam=1)


def transcribe_prefix(audio):
    # Keep a second of context and cut after a recognized word, never mid-word.
    segments, _ = whisper.transcribe(audio, language="en", beam_size=3,
                                    condition_on_previous_text=False, vad_filter=False,
                                    word_timestamps=True,
                                    initial_prompt=os.environ.get("STT_GLOSSARY") or None)
    words = [word for segment in segments for word in (segment.words or [])
             if word.end <= len(audio) / RATE - 1]
    if not words:
        raise ValueError("Could not find a safe speech boundary; reconnect to continue")
    return "".join(word.word for word in words).strip(), int(words[-1].end * RATE)


@lru_cache(maxsize=64)
def speak(text, voice_name=""):
    # Paradee speaks one voice (af_heart). The profile's voice name is kept in the
    # signature and in the cache key so a future multi-voice model needs no caller change.
    audio = voice(text, speed=float(os.environ.get("TTS_SPEED", "1.0")))
    if not len(audio):
        raise ValueError("Speech model returned no audio")
    stream = io.BytesIO()
    sf.write(stream, audio, SAMPLE_RATE, format="WAV")
    return stream.getvalue()


async def infer(function, value, *, speech=False):
    # Each model is serialized; generating a voice never locks transcription.
    async with (stt_lock if speech or function is transcribe else tts_lock):
        task = asyncio.create_task(asyncio.to_thread(function, value))
        try:
            return await asyncio.shield(task)
        except asyncio.CancelledError:
            await task  # Do not release the model lock while its thread still runs.
            raise


async def judge(ws, client, floor=None, *, speaking=False):
    try:
        response = await client.post(API + "/media/question", params={"speaking": str(speaking).lower()})
        response.raise_for_status()
        result = response.json()
        if result.get("warning"):
            await ws.send_json({"type": "warning", "message": result["warning"]})
        question = result.get("question")
        if question:
            if floor is not None:
                floor.set()
            await ws.send_json({"type": "question", "text": question, "speaker": result.get("speaker", "Examiner")})
            try:
                for sentence in re.split(r"(?<=[.!?])\s+", question):
                    await ws.send_bytes(await infer(lambda text: speak(text, result.get("voice", "")), sentence))
            except Exception:
                log.exception("synthesize question")
                await ws.send_json({"type": "warning", "message": "Voice is unavailable. Read the question on screen."})
            finally:
                await ws.send_json({"type": "judge_audio_end"})
    except (httpx.HTTPError, RuntimeError):
        log.exception("judge request failed")
        await ws.send_json({"type": "warning", "message": "The judge is unavailable. Your transcript was saved."})


async def receive_audio(ws, queue, deadline=None, floor=None):
    muted_since = None
    while True:
        remaining = deadline - time.time() if deadline is not None else 30
        if remaining <= 0:
            await queue.put(None)
            return
        try:
            message = await asyncio.wait_for(ws.receive(), min(30, remaining))
        except asyncio.TimeoutError:
            if deadline is not None and time.time() >= deadline:
                await queue.put(None)
                return
            raise
        if deadline is not None and time.time() >= deadline:
            await queue.put(None)
            return
        if message["type"] == "websocket.disconnect":
            await queue.put(None)
            return
        if message.get("text"):
            kind = json.loads(message["text"]).get("type")
            if kind == "playback_done" and floor is not None:
                floor.clear()
                muted_since = None
            if kind == "finish":
                await queue.put(None)
                return
            continue
        data = message.get("bytes", b"")
        if not data or len(data) > RATE * 2 or len(data) % 2:
            raise ValueError("Invalid audio frame")
        if floor is not None and floor.is_set():
            muted_since = muted_since or time.monotonic()
            if time.monotonic() - muted_since > 30:
                floor.clear()
                muted_since = None
            else:
                data = bytes(len(data))
        else:
            muted_since = None
        # Bounded backpressure prevents a slow model from consuming unlimited RAM.
        await asyncio.wait_for(queue.put(data), 10)


async def process_audio(ws, client, queue, floor=None):
    audio = np.empty(0, dtype=np.float32)
    active = False
    silence_samples = 0
    partial_at = time.monotonic()
    settled_text = ""      # Words of this turn that are already fixed.
    settled_samples = 0    # The prefix of `audio` that settled_text covers.
    judge_task = None
    try:
        while True:
            data = await queue.get()
            finishing = data is None
            if not finishing:
                block = np.frombuffer(data, dtype="<i2").astype(np.float32) / 32768
                audio = np.concatenate((audio, block))
                if len(audio) < RATE // 2:
                    continue
                voiced = await asyncio.to_thread(get_speech_timestamps, audio[-RATE // 2:], VadOptions(min_speech_duration_ms=100))
                if voiced:
                    if not active:
                        if judge_task and not judge_task.done() and not judge_task.cancelling() and not (floor and floor.is_set()):
                            # Cancellation waits for the model thread in infer();
                            # do not stall incoming audio while it unwinds.
                            judge_task.cancel()
                        await ws.send_json({"type": "speech_started"})
                    active = True
                    silence_samples = 0
                elif active:
                    silence_samples += len(block)
            # Endpointing follows audio time, so slow inference cannot invent silence.
            # Rate-limited like a partial: a turn with no clean word boundary yet must
            # not retry the word-timestamp pass on every 100 ms block.
            want_split = active and not finishing and silence_samples < RATE * END_SILENCE and len(audio) >= RATE * MAX_TURN and time.monotonic() - partial_at >= PARTIAL_INTERVAL
            ending = (active and (finishing or silence_samples >= RATE * END_SILENCE)) or (finishing and len(audio) >= RATE // 10 and np.max(np.abs(audio)) > 0.005)
            partial = active and queue.empty() and time.monotonic() - partial_at >= PARTIAL_INTERVAL
            if ending or partial or want_split:
                tail = audio[settled_samples:]
                split = False
                # Fix the words that are no longer going to change, so every later
                # pass decodes the last few seconds instead of the whole turn. Without
                # this the text falls further behind the longer somebody talks.
                if want_split or (partial and not ending and len(tail) >= RATE * SETTLE_AFTER):
                    try:
                        head, consumed = await infer(transcribe_prefix, tail, speech=True)
                        settled_text = (settled_text + " " + head).strip()
                        settled_samples += consumed
                        tail = audio[settled_samples:]
                        split = want_split
                    except ValueError:
                        pass  # No safe word boundary yet. Decode the tail whole instead.
                final = ending or split
                text = settled_text
                if len(tail) and not split:
                    text = (text + " " + await infer(transcribe if final else transcribe_fast, tail, speech=True)).strip()
                partial_at = time.monotonic()
                if text:
                    await ws.send_json({"type": "transcript_final" if final else "transcript_partial", "text": text})
                if final:
                    consumed = settled_samples if split else len(audio)
                    if text:
                        body = {"id": str(uuid.uuid4()), "text": text, "duration_ms": int(consumed * 1000 / RATE)}
                        # Stable ID makes an uncertain response safe to retry.
                        for attempt in range(3):
                            try:
                                response = await client.post(API + "/media/turn", json=body)
                                response.raise_for_status()
                                break
                            except httpx.TransportError:
                                if attempt == 2:
                                    raise
                        await ws.send_json({"type": "saved"})
                        if not finishing and not (floor and floor.is_set()) and (judge_task is None or judge_task.done()):
                            judge_task = asyncio.create_task(judge(ws, client, floor, speaking=split))
                    audio = audio[consumed:]
                    settled_text, settled_samples = "", 0
                    active = split
                    silence_samples = 0
            if finishing:
                await ws.send_json({"type": "drained"})
                return
            if not active:
                audio = audio[-RATE // 2:]
    finally:
        if judge_task and not judge_task.done():
            if not judge_task.cancelling():
                judge_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await judge_task


@app.websocket("/speech")
async def speech(ws: WebSocket):
    if ws.headers.get("origin") not in ORIGINS:
        await ws.close(code=1008)
        return
    await ws.accept()
    identity = None
    registered = False
    tasks = []
    try:
        hello = await asyncio.wait_for(ws.receive_json(), 10)
        ticket = hello.get("ticket", "")
        claims = jwt.decode(ticket, TOKEN, algorithms=["HS256"], audience="megamoot-media", options={"require": ["exp", "jti", "sub", "session_id", "organization_id"]})
        identity = claims["session_id"]
        if identity in connections or len(connections) >= CAPACITY:
            await ws.send_json({"type": "error", "message": "This session is already connected or speech capacity is full. Retry shortly."})
            return
        if whisper is None or voice is None:
            await ws.send_json({"type": "error", "message": load_error or "Speech models are loading. Retry shortly."})
            return
        # Reserve before the first await so simultaneous joins cannot race.
        connections.add(identity)
        registered = True
        forwarded = ws.headers.get("x-forwarded-for", "").split(",", 1)[0].strip()
        client_ip = forwarded or ws.client.host
        async with httpx.AsyncClient(timeout=30, headers={"Authorization": "Bearer " + ticket, "X-Media-Service-Token": TOKEN, "X-Speech-Client-IP": client_ip}) as verifier:
            verified = await verifier.get(API + "/media/session")
            verified.raise_for_status()
        exchange = verified.json()
        deadline = datetime.fromisoformat(exchange["ends_at"].replace("Z", "+00:00")).timestamp()
        async with httpx.AsyncClient(timeout=180, headers={"Authorization": "Bearer " + exchange["media_token"], "X-Media-Service-Token": TOKEN}) as client:
            await ws.send_json({"type": "ready"})
            queue = asyncio.Queue(maxsize=100)
            floor = asyncio.Event()
            tasks = [asyncio.create_task(receive_audio(ws, queue, deadline, floor)), asyncio.create_task(process_audio(ws, client, queue, floor))]
            done, pending = await asyncio.wait(tasks, return_when=asyncio.FIRST_EXCEPTION)
            for task in done:
                task.result()
            if time.time() >= deadline:
                response = await client.post(API + "/media/finish")
                response.raise_for_status()
    except WebSocketDisconnect:
        pass
    except Exception:
        log.exception("speech connection failed")
        try:
            await ws.send_json({"type": "error", "message": "Speech connection failed. Saved turns remain available. Reconnect to continue."})
        except RuntimeError:
            pass
    finally:
        for task in tasks:
            if not task.done():
                task.cancel()
        for task in tasks:
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await task
        if registered:
            connections.discard(identity)
        with contextlib.suppress(RuntimeError):
            await ws.close()
