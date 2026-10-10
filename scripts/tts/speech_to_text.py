import time
import queue
import threading
import numpy as np
import sounddevice as sd
import torch

from faster_whisper import WhisperModel
from silero_vad import load_silero_vad, get_speech_timestamps


# ============================================================
# SETTINGS
# ============================================================

SAMPLE_RATE = 16000

# Audio received from microphone every 100 ms
BLOCK_DURATION_MS = 100
BLOCK_SIZE = int(SAMPLE_RATE * BLOCK_DURATION_MS / 1000)

# Run partial transcription roughly every 700 ms
PARTIAL_INTERVAL = 0.7

# Stop an utterance after this much silence
END_SILENCE_SECONDS = 1.0

MODEL_NAME = "small.en"

DEVICE = "cuda"
COMPUTE_TYPE = "float16"

# If partial transcription becomes too expensive,
# increase this to 1.0 or 1.5 seconds.
MAX_PARTIAL_AUDIO_SECONDS = 15


# ============================================================
# GLOBALS
# ============================================================

audio_queue = queue.Queue()

recording_audio = []

speech_active = False
last_voice_time = None
utterance_start_time = None

stop_event = threading.Event()


# ============================================================
# LOAD MODELS
# ============================================================

print("\nLoading Faster-Whisper...")

load_start = time.perf_counter()

whisper_model = WhisperModel(
    MODEL_NAME,
    device=DEVICE,
    compute_type=COMPUTE_TYPE,
)

print(
    f"Faster-Whisper loaded in "
    f"{time.perf_counter() - load_start:.2f}s"
)

print("Loading Silero VAD...")

vad_model = load_silero_vad()

print("Silero VAD loaded.")


# ============================================================
# MICROPHONE CALLBACK
# ============================================================

def audio_callback(indata, frames, time_info, status):
    if status:
        print(status)

    audio_queue.put(indata[:, 0].copy())


# ============================================================
# VAD
# ============================================================

def contains_speech(audio):
    """
    Runs Silero VAD on an audio chunk.

    Returns True if speech is detected.
    """

    if len(audio) < 512:
        return False

    tensor = torch.from_numpy(audio).float()

    timestamps = get_speech_timestamps(
        tensor,
        vad_model,
        sampling_rate=SAMPLE_RATE,
        threshold=0.5,
    )

    return len(timestamps) > 0


# ============================================================
# WHISPER
# ============================================================

def transcribe_audio(audio):
    """
    Transcribe a numpy float32 audio array.
    """

    if len(audio) == 0:
        return "", 0

    start = time.perf_counter()

    segments, info = whisper_model.transcribe(
        audio,
        language="en",
        beam_size=1,
        vad_filter=False,
        condition_on_previous_text=False,
    )

    segments = list(segments)

    text = " ".join(
        segment.text.strip()
        for segment in segments
    ).strip()

    latency = time.perf_counter() - start

    return text, latency


# ============================================================
# PARTIAL TRANSCRIPTION THREAD
# ============================================================

def partial_worker():
    global recording_audio

    last_partial_text = ""

    while not stop_event.is_set():

        time.sleep(PARTIAL_INTERVAL)

        if not speech_active:
            last_partial_text = ""
            continue

        if not recording_audio:
            continue

        audio = np.concatenate(recording_audio)

        # Only use recent audio for partial transcription.
        max_samples = int(
            MAX_PARTIAL_AUDIO_SECONDS * SAMPLE_RATE
        )

        if len(audio) > max_samples:
            audio = audio[-max_samples:]

        text, latency = transcribe_audio(audio)

        if text and text != last_partial_text:
            print(
                f"\rPartial: {text} "
                f"[{latency:.2f}s]",
                end="",
                flush=True,
            )

            last_partial_text = text


# ============================================================
# MAIN
# ============================================================

def main():
    global speech_active
    global last_voice_time
    global utterance_start_time
    global recording_audio

    print("\n======================================")
    print("LIVE SPEECH TO TEXT")
    print("======================================")

    print(f"Model:   {MODEL_NAME}")
    print(f"Device:  {DEVICE}")
    print(f"Compute: {COMPUTE_TYPE}")

    print("\nSpeak into your microphone.")
    print("Press Ctrl+C to stop.\n")

    partial_thread = threading.Thread(
        target=partial_worker,
        daemon=True,
    )

    partial_thread.start()

    vad_buffer = []

    # Silero performs better when we analyse a little
    # more audio than just one tiny callback block.
    vad_window_samples = int(0.5 * SAMPLE_RATE)

    try:

        with sd.InputStream(
            samplerate=SAMPLE_RATE,
            channels=1,
            dtype="float32",
            blocksize=BLOCK_SIZE,
            callback=audio_callback,
        ):

            while True:

                block = audio_queue.get()

                vad_buffer.append(block)

                combined_vad = np.concatenate(vad_buffer)

                # Keep roughly the last 0.5 sec for VAD
                if len(combined_vad) > vad_window_samples:
                    combined_vad = combined_vad[
                        -vad_window_samples:
                    ]

                    vad_buffer = [combined_vad]

                voice_detected = contains_speech(
                    combined_vad
                )

                current_time = time.perf_counter()

                # -----------------------------------------
                # SPEECH STARTED
                # -----------------------------------------

                if voice_detected:

                    last_voice_time = current_time

                    if not speech_active:

                        speech_active = True
                        utterance_start_time = current_time
                        recording_audio = []

                        print(
                            "\n\n🎤 Speech detected..."
                        )

                    recording_audio.append(block)

                # -----------------------------------------
                # CURRENTLY SPEAKING
                # -----------------------------------------

                elif speech_active:

                    # Keep adding silence briefly
                    # so endings don't get chopped.
                    recording_audio.append(block)

                    silence_duration = (
                        current_time - last_voice_time
                    )

                    # -------------------------------------
                    # END OF SPEECH
                    # -------------------------------------

                    if silence_duration >= END_SILENCE_SECONDS:

                        speech_active = False

                        audio = np.concatenate(
                            recording_audio
                        )

                        audio_duration = (
                            len(audio) / SAMPLE_RATE
                        )

                        print(
                            "\n\nProcessing final transcript..."
                        )

                        final_start = time.perf_counter()

                        text, whisper_latency = (
                            transcribe_audio(audio)
                        )

                        total_latency = (
                            time.perf_counter()
                            - final_start
                        )

                        realtime_factor = (
                            whisper_latency /
                            audio_duration
                            if audio_duration > 0
                            else 0
                        )

                        speed = (
                            audio_duration /
                            whisper_latency
                            if whisper_latency > 0
                            else 0
                        )

                        print("\n")
                        print("=" * 60)
                        print("FINAL")
                        print("=" * 60)

                        print(f"\n{text}")

                        print("\nPerformance")
                        print(
                            f"Audio duration: "
                            f"{audio_duration:.2f}s"
                        )

                        print(
                            f"Whisper latency: "
                            f"{whisper_latency:.3f}s"
                        )

                        print(
                            f"Total final latency: "
                            f"{total_latency:.3f}s"
                        )

                        print(
                            f"RTF: "
                            f"{realtime_factor:.3f}"
                        )

                        print(
                            f"Speed: "
                            f"{speed:.2f}x realtime"
                        )

                        print("=" * 60)

                        recording_audio = []

    except KeyboardInterrupt:

        print("\n\nStopping...")

        stop_event.set()


if __name__ == "__main__":
    main()