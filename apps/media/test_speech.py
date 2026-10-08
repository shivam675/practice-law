"""Run: python -m unittest test_speech -v (inside the media container)."""
import asyncio
import time
import unittest
from unittest.mock import AsyncMock, patch

import numpy as np
import app


class SpeechChecks(unittest.IsolatedAsyncioTestCase):
    async def test_finish_preserves_short_last_words(self):
        queue = asyncio.Queue()
        await queue.put(np.full(4800, 3000, dtype='<i2').tobytes())
        await queue.put(None)
        ws = AsyncMock()
        client = AsyncMock()
        client.post.return_value.raise_for_status = lambda: None
        with patch.object(app, 'transcribe', return_value='Thank you.') as transcribe:
            await app.process_audio(ws, client, queue, {'exp': time.time() + 60})
        transcribe.assert_called_once()
        self.assertEqual(client.post.call_args.kwargs['json']['text'], 'Thank you.')
        self.assertEqual(ws.send_json.call_args.args[0]['type'], 'drained')


if __name__ == '__main__':
    unittest.main()
