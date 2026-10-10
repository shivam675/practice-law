import torch
import soundfile as sf
from kittenml import KittenTTS
import time

print("CUDA available:", torch.cuda.is_available())

device = "cuda" if torch.cuda.is_available() else "cpu"
print("Using:", device)

m = KittenTTS(
    "KittenML/kitten-tts-2",
    device=device
)

start = time.perf_counter()

audio = m.generate(
    "We started building our first AI copilot in late January  2026. The goal was simple. We wanted to ease users' life by asking them to describe a task in plain language and have the assistant complete it for them. Over time, we tried several approaches. Some were simple. Others gave us more control over the assistant. Each approach improved something, but none fully solved the problem. Our biggest challenge was getting local language models to perform tasks reliably. They sometimes selected the wrong tools, invented values, or failed to complete requests involving multiple steps. Initially, I thought the models were not capable enough. Later, we discovered that the way we built the system was just as important as the model itself.",
    voice="Bruno"
)

print(f"Generation time: {time.perf_counter() - start:.2f}s")

sf.write("output.wav", audio, m.sample_rate)
print("Saved output.wav")