# Run MegaMoot on Linux without Docker

Three processes, one database. Compose is a convenience, not a requirement.

## 1. Install the dependencies

Install these packages with your distribution package manager:

1. PostgreSQL 16 or 17.
2. The PostgreSQL extensions `pgvector`, `pgcrypto` and `pg_trgm`. Debian and
   Ubuntu supply `postgresql-17-pgvector`. Fedora supplies `pgvector_17`.
3. Go 1.23 or later.
4. Python 3.12 or later, with the `venv` module.
5. Node.js 20 or later, with npm.

Do not install Redis. Do not install MinIO. The Go code reads `REDIS_URL` into
the configuration structure and uses it nowhere. The blob driver writes to the
local disk.

## 2. Create the database

```bash
sudo -u postgres psql -c "CREATE USER megamoot WITH PASSWORD 'megamoot_dev_password';"
sudo -u postgres psql -c "CREATE DATABASE megamoot OWNER megamoot;"
sudo -u postgres psql -d megamoot -c "CREATE EXTENSION IF NOT EXISTS vector;"
```

Migration `0001_foundation.sql` also creates `pgcrypto` and `pg_trgm`. The
migration runner creates them. You must create `vector` first only if the
`megamoot` user is not a superuser.

## 3. Write the environment file

The API reads the process environment. It does not read a `.env` file. Copy the
example file and change five values:

```bash
cp .env.example .env
sed -i 's/^POSTGRES_HOST=postgres/POSTGRES_HOST=localhost/' .env
sed -i 's|^DATABASE_URL=.*|DATABASE_URL=postgres://megamoot:megamoot_dev_password@localhost:5432/megamoot?sslmode=disable|' .env
sed -i 's|^AI_SERVICE_URL=.*|AI_SERVICE_URL=http://localhost:8100|' .env
sed -i 's|^MEDIA_SERVICE_URL=.*|MEDIA_SERVICE_URL=http://localhost:8400|' .env
sed -i 's|^BLOB_FS_ROOT=.*|BLOB_FS_ROOT=./data/blobs|' .env
mkdir -p data/blobs
```

`BLOB_FS_ROOT` defaults to `/var/lib/megamoot/blobs`. A normal user account
cannot write to that directory.

## 4. Start the AI plane

Open a terminal. Run these commands:

```bash
cd apps/ai
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt
AI_SERVICE_TOKEN=dev_ai_service_token_change_me uvicorn app.main:app --port 8100
```

The token value must match `AI_SERVICE_TOKEN` in the `.env` file.

## 5. Start the control plane

Open a second terminal. Run these commands:

```bash
set -a && . ./.env && set +a
cd apps/api && go run ./cmd/api
```

`set -a` exports each variable that the file defines. The API applies all
migrations at start. It then seeds the demonstration organization, users and
team.

## 6. Start the web app

Open a third terminal. Run these commands:

```bash
cd apps/web
npm install
npm run dev
```

Open `http://localhost:5173`. The Vite development server sends `/api` to
`http://localhost:8080` and `/speech` to `http://localhost:8400`. These are the
defaults in `vite.config.ts`. You do not need to set `VITE_API_PROXY` or
`VITE_MEDIA_PROXY`.

## 7. Start the media plane (optional)

The media plane downloads speech models on first use. The control plane starts
without it. Start it only when you need speech:

```bash
cd apps/media
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt
MEDIA_SERVICE_TOKEN=local_speech_development_only_change_me \
CONTROL_API_URL=http://localhost:8080/api/v1 \
MEDIA_ORIGINS=http://localhost:5173 STT_DEVICE=cpu \
uvicorn app:app --port 8400
```

## What this setup does not give you

The compose file isolates the AI plane on a network with no route out, makes its
filesystem read only, and caps its memory and process count. See
`docs/security.md`. A plain `uvicorn` process has none of those limits. Use this
setup for development only.
