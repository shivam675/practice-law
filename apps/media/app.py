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
from contextlib import asynccontextmanager
from functools import lru_cache

import httpx
import jwt
import numpy as np
import soundfile as sf
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from faster_whisper import WhisperModel
from faster_whisper.vad import VadOptions, get_speech_timestamps
from kokoro import KPipeline

log = logging.getLogger("media")
RATE = 16000
TOKEN = os.environ["MEDIA_SERVICE_TOKEN"]
API = os.environ.get("CONTROL_API_URL", "http://api:8080/api/v1")
ORIGINS = os.environ.get("MEDIA_ORIGINS", "http://localhost:5173").split(",")
CAPACITY = int(os.environ.get("MAX_CONCURRENT_LIVE_SESSIONS", "3"))
END_SILENCE = float(os.environ.get("STT_END_SILENCE_SECONDS", "0.65"))
PARTIAL_INTERVAL = float(os.environ.get("STT_PARTIAL_INTERVAL_SECONDS", "2.5"))
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
        voice = KPipeline(lang_code="a", device=os.environ.get("TTS_DEVICE", "cpu"))
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
    return {"ready": whisper is not None and voice is not None, "stt": "faster-whisper", "tts": "kokoro", "error": load_error}


def transcribe(audio):
    started = time.monotonic()
    # Endpointing already applied VAD. A second pass can remove quiet words.
    segments, _ = whisper.transcribe(audio, language="en", beam_size=3,
                                    condition_on_previous_text=False, vad_filter=False,
                                    initial_prompt=os.environ.get("STT_GLOSSARY") or None)
    text = " ".join(segment.text.strip() for segment in segments).strip()
    log.info("stt audio_s=%.2f inference_s=%.2f", len(audio) / RATE, time.monotonic() - started)
    return text


@lru_cache(maxsize=64)
def speak(text):
    chunks = [audio.numpy() for _, _, audio in voice(text, voice=os.environ.get("TTS_VOICE", "af_heart")) if audio is not None]
    if not chunks:
        raise ValueError("Speech model returned no audio")
    stream = io.BytesIO()
    sf.write(stream, np.concatenate(chunks), 24000, format="WAV")
    return stream.getvalue()


async def infer(function, value):
    # Each model is serialized; generating a voice never locks transcription.
    async with (stt_lock if function is transcribe else tts_lock):
        task = asyncio.create_task(asyncio.to_thread(function, value))
        try:
            return await asyncio.shield(task)
        except asyncio.CancelledError:
            await task  # Do not release the model lock while its thread still runs.
            raise


async def judge(ws, client):
    try:
        response = await client.post(API + "/media/question")
        response.raise_for_status()
        result = response.json()
        if result.get("warning"):
            await ws.send_json({"type": "warning", "message": result["warning"]})
        question = result.get("question")
        if question:
            await ws.send_json({"type": "question", "text": question})
            try:
                for sentence in re.split(r"(?<=[.!?])\s+", question):
                    await ws.send_bytes(await infer(speak, sentence))
            except Exception:
                log.exception("synthesize question")
                await ws.send_json({"type": "warning", "message": "Voice is unavailable. Read the question on screen."})
    except (httpx.HTTPError, RuntimeError):
        log.exception("judge request failed")


async def receive_audio(ws, queue):
    while True:
        message = await asyncio.wait_for(ws.receive(), 30)
        if message["type"] == "websocket.disconnect":
            await queue.put(None)
            return
        if message.get("text"):
            if json.loads(message["text"]).get("type") == "finish":
                await queue.put(None)
                return
            continue
        data = message.get("bytes", b"")
        if not data or len(data) > RATE * 2 or len(data) % 2:
            raise ValueError("Invalid audio frame")
        # Bounded backpressure prevents a slow model from consuming unlimited RAM.
        await asyncio.wait_for(queue.put(data), 10)


async def process_audio(ws, client, queue, claims):
    audio = np.empty(0, dtype=np.float32)
    active = False
    silence_samples = 0
    partial_at = time.monotonic()
    judge_task = None
    try:
        while True:
            data = await queue.get()
            finishing = data is None
            if time.time() >= claims["exp"]:
                raise ValueError("Speech ticket expired")
            if not finishing:
                block = np.frombuffer(data, dtype="<i2").astype(np.float32) / 32768
                audio = np.concatenate((audio, block))
                if len(audio) < RATE // 2:
                    continue
                voiced = await asyncio.to_thread(get_speech_timestamps, audio[-RATE // 2:], VadOptions(min_speech_duration_ms=100))
                if voiced:
                    if not active:
                        if judge_task and not judge_task.done() and not judge_task.cancelling():
                            # Cancellation waits for the model thread in infer();
                            # do not stall incoming audio while it unwinds.
                            judge_task.cancel()
                        await ws.send_json({"type": "speech_started"})
                    active = True
                    silence_samples = 0
                elif active:
                    silence_samples += len(block)
            # Endpointing follows audio time, so slow inference cannot invent silence.
            final = (active and (finishing or silence_samples >= RATE * END_SILENCE or len(audio) >= RATE * 25)) or (finishing and len(audio) >= RATE // 10 and np.max(np.abs(audio)) > 0.005)
            partial = active and queue.empty() and time.monotonic() - partial_at >= PARTIAL_INTERVAL
            if final or partial:
                text = await infer(transcribe, audio)
                partial_at = time.monotonic()
                if text:
                    await ws.send_json({"type": "transcript_final" if final else "transcript_partial", "text": text})
                if final:
                    if text:
                        body = {"id": str(uuid.uuid4()), "text": text, "duration_ms": int(len(audio) * 1000 / RATE)}
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
                        if not finishing and (judge_task is None or judge_task.done()):
                            judge_task = asyncio.create_task(judge(ws, client))
                    audio = np.empty(0, dtype=np.float32)
                    active = False
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
        claims = jwt.decode(ticket, TOKEN, algorithms=["HS256"], audience="megamoot-media", options={"require": ["exp", "sub", "session_id", "organization_id"]})
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
        async with httpx.AsyncClient(timeout=180, headers={"Authorization": "Bearer " + ticket, "X-Media-Service-Token": TOKEN}) as client:
            verified = await client.get(API + "/media/session")
            verified.raise_for_status()
            await ws.send_json({"type": "ready"})
            queue = asyncio.Queue(maxsize=100)
            tasks = [asyncio.create_task(receive_audio(ws, queue)), asyncio.create_task(process_audio(ws, client, queue, claims))]
            done, pending = await asyncio.wait(tasks, return_when=asyncio.FIRST_EXCEPTION)
            for task in done:
                task.result()
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
