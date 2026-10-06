# Deployment

## Development

```bash
cp .env.example .env
docker compose up -d postgres redis
docker compose up -d --build api
```

Ollama runs on the host, not in Compose, so it keeps direct GPU access.
Containers reach it at `host.docker.internal:11434`.

Blobs go to `./data/blobs` through the filesystem adapter. MinIO was dropped:
its images now require registry authentication, and a bind-mounted directory is
a smaller dependency. Production switches `BLOB_DRIVER` to `s3`.

Postgres is published on host port 55432, because 5432 falls inside the Windows
reserved port range on some machines. Inside the Compose network it is 5432.

## Production V1 — single box plus one GPU box

```
            internet
               |
            Caddy (TLS, reverse proxy)
               |
      +--------+---------+
      |                  |
   apps/web           apps/api  (2 replicas)
   static                 |
                          +--> Postgres 17 + pgvector
                          +--> Redis
                          +--> S3-compatible object storage
                          |
                          +--> gRPC --> apps/ai     (GPU box)
                          +--> ticket -> apps/media (GPU box, public WS)
```

The media plane needs a public WebSocket endpoint because the browser dials it
directly. Caddy terminates TLS for it on a separate hostname.

## GPU box capacity

16 GB VRAM, resident: Qwen3-8B Q5 (6 GB), Qwen3-4B Q5 (3 GB), faster-whisper
small.en (1 GB), Kokoro (0.5 GB). Roughly 11 GB, leaving headroom for KV cache
and batching.

**Concurrent live sessions: 3 to 5.** `MAX_CONCURRENT_LIVE_SESSIONS` enforces
this. Session 6 queues in the waiting room rather than degrading the other five.

Qwen3-30B-A3B is loaded only for offline grading, scheduled when no live session
is running. Keeping it resident during live sessions is not possible on 16 GB.

Non-live traffic (login, dashboards, case reading, uploads) handles well beyond
50 concurrent users on the CPU box alone. The GPU is the only scarce resource.

## Scaling path

| Trigger | Action |
|---|---|
| Live sessions exceed 5 concurrent | Second GPU box; media plane is already stateless per session |
| Grading backlog | Dedicated grading box, or burst to a hosted model |
| Over 2 API replicas | Redis pub/sub for WebSocket fan-out becomes mandatory |
| Over 500 writes per second | Partition `session_events` by month, batch segment inserts |
| Multiple institutions | Per-org capacity quotas on `session_slots` |

## Backups

- Postgres: nightly `pg_dump` plus WAL archiving to object storage.
- Object storage: versioning on, lifecycle rule matching the retention policy.
- Restore drill before the pilot. An untested backup is not a backup.

## Observability

OpenTelemetry traces propagated across Go, Python and the browser session id,
because a slow judge response needs to be attributed to VAD, STT, the monitor or
TTS. Structured JSON logs (`slog`, `structlog`) with request id and session id
on every line. `ai_requests` in Postgres for cost and latency, since it is also
the billing source.
