"""Run: python -m unittest test_speech -v (inside the media container)."""
import asyncio
import time
import threading
import unittest
from unittest.mock import AsyncMock, patch

import numpy as np
import app


class SpeechChecks(unittest.IsolatedAsyncioTestCase):
    async def test_judge_announces_floor_and_synthesis_end(self):
        ws, client = AsyncMock(), AsyncMock()
        client.post.return_value.raise_for_status = lambda: None
        client.post.return_value.json = lambda: {"question": "How does that relate?", "speaker": "Judge"}
        floor = asyncio.Event()
        with patch.object(app, 'speak', return_value=b'audio'):
            await app.judge(ws, client, floor, speaking=True)
        self.assertTrue(floor.is_set())
        self.assertEqual(client.post.call_args.kwargs['params'], {"speaking": "true"})
        self.assertEqual([c.args[0]['type'] for c in ws.send_json.call_args_list], ['question', 'judge_audio_end'])

    async def test_playback_ack_releases_floor_and_echo_is_discarded(self):
        floor = asyncio.Event()
        floor.set()
        ws = AsyncMock()
        ws.receive.side_effect = [
            {"type": "websocket.receive", "bytes": b'\x01\x02' * 800},
            {"type": "websocket.receive", "text": '{"type":"playback_done"}'},
            {"type": "websocket.receive", "bytes": b'\x01\x02' * 800},
            {"type": "websocket.disconnect"},
        ]
        queue = asyncio.Queue()
        await app.receive_audio(ws, queue, floor=floor)
        self.assertEqual(await queue.get(), bytes(1600))
        self.assertEqual(await queue.get(), b'\x01\x02' * 800)
        self.assertFalse(floor.is_set())

    async def test_deadline_stops_capture_and_drains_queued_audio(self):
        queue = asyncio.Queue()
        ws = AsyncMock()
        async def wait_for_audio():
            await asyncio.sleep(1)
        ws.receive.side_effect = wait_for_audio
        await app.receive_audio(ws, queue, time.time() + .02)
        self.assertIsNone(await queue.get())

    async def test_long_speech_keeps_uncommitted_word_audio(self):
        queue = asyncio.Queue()
        for _ in range(26):
            await queue.put(np.full(app.RATE, 3000, dtype='<i2').tobytes())
        await queue.put(None)
        ws, client = AsyncMock(), AsyncMock()
        client.post.return_value.raise_for_status = lambda: None
        with patch.object(app, 'get_speech_timestamps', return_value=[{}]), \
             patch.object(app, 'MAX_TURN', 25), patch.object(app, 'PARTIAL_INTERVAL', 0), \
             patch.object(app, 'judge', new_callable=AsyncMock), \
             patch.object(app, 'transcribe_prefix', return_value=('Earlier words.', 24 * app.RATE)), \
             patch.object(app, 'transcribe', return_value='Remaining words.') as tail:
            await app.process_audio(ws, client, queue)
        self.assertEqual(len(tail.call_args.args[0]), 2 * app.RATE)
        self.assertEqual([c.kwargs['json']['text'] for c in client.post.call_args_list], ['Earlier words.', 'Remaining words.'])

    async def test_continuous_speech_requests_urgent_monitor(self):
        queue = asyncio.Queue()
        for _ in range(10):
            await queue.put(np.full(app.RATE, 3000, dtype='<i2').tobytes())
        await queue.put(None)
        ws, client = AsyncMock(), AsyncMock()
        client.post.return_value.raise_for_status = lambda: None
        with patch.object(app, 'get_speech_timestamps', return_value=[{}]), \
             patch.object(app, 'MAX_TURN', 8), patch.object(app, 'PARTIAL_INTERVAL', 0), \
             patch.object(app, 'transcribe_prefix', return_value=('Unrelated holiday story.', 7 * app.RATE)), \
             patch.object(app, 'transcribe', return_value='Remaining words.'), \
             patch.object(app, 'judge', new_callable=AsyncMock) as judge:
            await app.process_audio(ws, client, queue)
        self.assertTrue(judge.call_args.kwargs['speaking'])

    async def test_listening_does_not_wait_for_voice_generation(self):
        release = threading.Event()
        started = asyncio.Event()
        loop = asyncio.get_running_loop()
        def slow_voice(_):
            loop.call_soon_threadsafe(started.set)
            release.wait(3)
        task = asyncio.create_task(app.infer(slow_voice, "question"))
        await started.wait()
        try:
            with patch.object(app, 'transcribe', return_value='Still listening'):
                result = await asyncio.wait_for(app.infer(app.transcribe, None), .5)
            self.assertEqual(result, 'Still listening')
        finally:
            release.set()
            await task

    async def test_finish_preserves_short_last_words(self):
        queue = asyncio.Queue()
        await queue.put(np.full(4800, 3000, dtype='<i2').tobytes())
        await queue.put(None)
        ws = AsyncMock()
        client = AsyncMock()
        client.post.return_value.raise_for_status = lambda: None
        with patch.object(app, 'transcribe', return_value='Thank you.') as transcribe:
            await app.process_audio(ws, client, queue)
        transcribe.assert_called_once()
        self.assertEqual(client.post.call_args.kwargs['json']['text'], 'Thank you.')
        self.assertEqual(ws.send_json.call_args.args[0]['type'], 'drained')

    def test_readiness_fails_until_both_models_load(self):
        with patch.object(app, 'whisper', None), patch.object(app, 'voice', None):
            self.assertEqual(app.ready().status_code, 503)


if __name__ == '__main__':
    unittest.main()
