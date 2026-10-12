#!/usr/bin/env bash
# Restores a backup written by backup.sh. This REPLACES the current database
# and uploaded files.
#
#   scripts/restore.sh /var/backups/megamoot/db-20261011T021500Z.dump \
#                      /var/backups/megamoot/blobs-20261011T021500Z.tgz
#
# Rehearse it on a spare machine before the pilot: a backup that has never
# been restored is a hope, not a backup.
set -euo pipefail
cd "$(dirname "$0")/.."

dump=${1:?usage: restore.sh DB_DUMP BLOBS_TGZ}
blobs=${2:?usage: restore.sh DB_DUMP BLOBS_TGZ}
[ -f "$dump" ] && [ -f "$blobs" ] || { echo "backup files not found" >&2; exit 1; }
compose="docker compose -f docker-compose.production.yml"
project=${COMPOSE_PROJECT_NAME:-megamoot}  # the name: in docker-compose.production.yml

read -r -p "This replaces the database and uploads of project '$project'. Type RESTORE to continue: " answer
[ "$answer" = RESTORE ] || { echo "cancelled"; exit 1; }

$compose stop web media api
$compose up -d postgres
until $compose exec -T postgres sh -c 'pg_isready -U "$POSTGRES_USER"' >/dev/null 2>&1; do sleep 1; done
$compose exec -T postgres sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner' < "$dump"
docker run --rm -v "${project}_blobdata:/blobs" -v "$(cd "$(dirname "$blobs")" && pwd):/in:ro" alpine \
  sh -c "find /blobs -mindepth 1 -delete && tar xzf /in/$(basename "$blobs") -C /blobs && chown -R 65532:65532 /blobs"
$compose up -d
echo "restored $dump and $blobs"
