import time
from paradee import Paradee, SAMPLE_RATE
import sounddevice as sd
from queue import Queue
from threading import Thread

loading = time.perf_counter()
tts = Paradee()
audio_queue = Queue()
print(f"Model loaded in {time.perf_counter() - loading:.2f}s")

generation = time.perf_counter()
print(f"Generation started at {generation:.2f}s")

def generate_speech(sentences):
    print(f"Generating speech for {len(sentences)} sentences.")
    for sentence in sentences:
        audio = tts(sentence)
        audio_queue.put(audio)
        print(f"Generated speech for: {sentence}")

    audio_queue.put(None)
    
def play_speech():
    while True:
        
        audio = audio_queue.get()
        if audio is None:
            break

        sd.play(audio, SAMPLE_RATE)
        sd.wait()

sentences = ""
with open("content.txt", "r") as f:
    sentences = f.readlines()

sentences = [sentence.strip() for sentence in sentences if sentence.strip()]

producer = Thread(target=generate_speech, args=(sentences,))
consumer = Thread(target=play_speech)

consumer.start()
producer.start()

print("All sentences generated and played.")

producer.join()
consumer.join()