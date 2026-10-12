# Deploy for a pilot

One Linux server with the GPU, a public domain, and Docker Compose. Read it
top to bottom once before starting; the order matters.

## 1. The server

- Ubuntu 22.04 or 24.04, an NVIDIA GPU with 16 GB, 8 or more CPU cores, 32 GB
  RAM, 100 GB disk. Speech recognition runs on the CPU; the models run on the
  GPU.
- NVIDIA driver installed (`nvidia-smi` works).
- Docker Engine with the Compose plugin.
- A DNS `A` record for the domain pointing at the server, and ports 80 and 443
  open to the internet. Caddy obtains the certificate itself. Without HTTPS the
  browser will not give the page a microphone.
- Students use exactly `https://DOMAIN`. A `www.` address is a different
  origin and the speech service refuses it.

## 2. Ollama on the host

Install Ollama, then make it listen on the Docker bridge only, never on the
public interface:

```bash
sudo systemctl edit ollama
# [Unit]
# After=docker.service
# [Service]
# Environment="OLLAMA_HOST=172.17.0.1:11434"
# Environment="OLLAMA_KEEP_ALIVE=10m"
sudo systemctl restart ollama
OLLAMA_URL=http://172.17.0.1:11434 scripts/prepare-models.sh
```

`172.17.0.1` is the host's address on `docker0`. The API container reaches it
as `host.docker.internal`. Check the firewall still refuses 11434 from
outside: `sudo ufw deny 11434` if you run ufw.

## 3. Configuration

```bash
git clone <repo> /opt/megamoot && cd /opt/megamoot
scripts/new-production-env.sh moot.example.edu "Example Law School" admin@example.edu
```

This writes `.env` with fresh secrets and prints a temporary administrator
password. Copy `.env` somewhere safe and separate: `CONFIG_ENCRYPTION_KEY` is
the only way to read the stored provider settings. The API refuses to start
in production with any development value.

## 4. Start

```bash
docker compose -f docker-compose.production.yml up -d --build
docker compose -f docker-compose.production.yml ps    # every service healthy
curl -fsS https://moot.example.edu/healthz
```

The first build downloads the speech models into the media image, so later
starts need no internet. Migrations run on start.

## 5. Bind the models

Sign in as the administrator, change the password, open `/admin/models`:

| Setting | Value |
|---|---|
| Provider | `openai_compatible`, `http://host.docker.internal:11434/v1`, no key |
| `monitor` | `qwen3-live`, reasoning `none`, max tokens 512, timeout 12000 ms |
| `judge` | `qwen3-live`, reasoning `none`, max tokens 512, timeout 12000 ms |
| `grader` | `qwen3-8b-live`, reasoning `none`, max tokens 2048, timeout 120000 ms |
| `embedding` | `bge-m3`, timeout 30000 ms |

Use the connection test on the page. Then remove `SEED_ADMIN_PASSWORD` from
`.env`; it is only read when the organisation is first created.

## 6. Backups

```bash
sudo crontab -e
# 15 2 * * * BACKUP_REMOTE=backup@other-host:/srv/megamoot /opt/megamoot/scripts/backup.sh >> /var/log/megamoot-backup.log 2>&1
```

Each run writes the database (`pg_dump -Fc`), the uploaded files, and a copy
of `.env`, keeps 14 days, and copies them off the box when `BACKUP_REMOTE` is
set. Rehearse `scripts/restore.sh` on another machine before the pilot.

## 7. Before students arrive

- Run one full round yourself from a student account on a phone and on a
  laptop: microphone, judge question, voice, finish, feedback form.
- Two live rooms are admitted at a time (`MAX_CONCURRENT_LIVE_SESSIONS=2`).
  Others see "All speaking rooms are busy". Raise it only after a load test.
- Grading runs only when a teacher presses Autograde, and waits while a live
  round runs.
- Audio is never stored; transcripts are. Get the consent text and retention
  period approved (see `docs/security.md`, section 7) before the first student.

## Operating

| Task | Command |
|---|---|
| Logs | `docker compose -f docker-compose.production.yml logs -f api media` |
| Update | `git pull && docker compose -f docker-compose.production.yml up -d --build` |
| Model calls in the last hour | `SELECT purpose, status, count(*) FROM ai_requests WHERE created_at > now() - interval '1 hour' GROUP BY 1, 2;` |
| Student feedback | `/feedback` in the app |

An update restarts the API. A grading run in progress is closed as
interrupted, and the teacher presses Autograde again. Avoid updating during a
live round: the student reconnects and continues, but loses the current turn.
