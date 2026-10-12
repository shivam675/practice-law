#!/usr/bin/env bash
# Writes .env for a production deploy with fresh random secrets.
#
#   scripts/new-production-env.sh moot.example.edu "Example Law School" admin@example.edu
#
# It refuses to overwrite an existing .env: replacing CONFIG_ENCRYPTION_KEY
# makes every stored provider credential unreadable, and replacing
# POSTGRES_PASSWORD locks the API out of an existing database.
set -euo pipefail
cd "$(dirname "$0")/.."

domain=${1:?usage: new-production-env.sh DOMAIN "ORGANISATION NAME" ADMIN_EMAIL}
org=${2:?organisation name required}
admin=${3:?administrator email required}
[ -e .env ] && { echo ".env already exists; refusing to overwrite it" >&2; exit 1; }

secret() { openssl rand -hex 32; }
admin_password=$(openssl rand -base64 24 | tr -d '/+=' | cut -c1-24)

umask 077
cat > .env <<EOF
# Generated $(date -u +%Y-%m-%dT%H:%M:%SZ) by scripts/new-production-env.sh.
# Back this file up somewhere safe and separate from the database backups.
DOMAIN=${domain}
LOG_LEVEL=info

POSTGRES_USER=megamoot
POSTGRES_DB=megamoot
POSTGRES_PASSWORD=$(secret)

JWT_SIGNING_KEY=$(secret)
CONFIG_ENCRYPTION_KEY=$(secret)
AI_SERVICE_TOKEN=$(secret)
MEDIA_SERVICE_TOKEN=$(secret)

ACCESS_TOKEN_TTL=10m
REFRESH_TOKEN_TTL=720h

# Two live rooms per GPU box with CPU speech recognition. Raise only after a
# load test; the API and the speech service both read this value.
MAX_CONCURRENT_LIVE_SESSIONS=2

# Created on first start only. Sign in, change the password, then remove it.
SEED_ORG_NAME=${org}
SEED_ADMIN_EMAIL=${admin}
SEED_ADMIN_PASSWORD=${admin_password}
EOF

echo "Wrote .env for https://${domain}"
echo "First administrator: ${admin}"
echo "Temporary password:  ${admin_password}"
echo "Change it after the first sign-in."
