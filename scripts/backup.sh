#!/usr/bin/env bash
# Backs up the database and uploaded files. Run nightly from cron:
#
#   15 2 * * * /opt/megamoot/scripts/backup.sh >> /var/log/megamoot-backup.log 2>&1
#
# BACKUP_DIR      where archives go            (default /var/backups/megamoot)
# BACKUP_KEEP     days of archives to keep     (default 14)
# BACKUP_REMOTE   rsync target off this box, e.g. backup@host:/srv/megamoot.
#                 A backup on the same disk as the database is not a backup.
set -euo pipefail
cd "$(dirname "$0")/.."

dir=${BACKUP_DIR:-/var/backups/megamoot}
keep=${BACKUP_KEEP:-14}
stamp=$(date -u +%Y%m%dT%H%M%SZ)
compose="docker compose -f docker-compose.production.yml"
project=${COMPOSE_PROJECT_NAME:-megamoot}  # the name: in docker-compose.production.yml
mkdir -p "$dir"

# Custom format: compressed, and pg_restore can restore one table from it.
$compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > "$dir/db-$stamp.dump.part"
mv "$dir/db-$stamp.dump.part" "$dir/db-$stamp.dump"

docker run --rm -v "${project}_blobdata:/blobs:ro" -v "$dir:/out" alpine \
  tar czf "/out/blobs-$stamp.tgz.part" -C /blobs .
mv "$dir/blobs-$stamp.tgz.part" "$dir/blobs-$stamp.tgz"

# The env file holds the key that decrypts stored provider credentials.
install -m 600 .env "$dir/env-$stamp"

find "$dir" -maxdepth 1 \( -name 'db-*.dump' -o -name 'blobs-*.tgz' -o -name 'env-*' \) -mtime +"$keep" -delete

if [ -n "${BACKUP_REMOTE:-}" ]; then
  rsync -a "$dir/" "$BACKUP_REMOTE/"
fi
echo "$(date -u +%FT%TZ) backup $stamp written to $dir"
