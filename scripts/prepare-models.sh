#!/usr/bin/env bash
# Pulls the pilot models into the host's Ollama and creates the aliases the
# model bindings point at. Linux counterpart of prepare-live-model.ps1.
#
#   qwen3-live     Qwen3 4B Instruct, 16K context   monitor and judge tiers
#   qwen3-8b-live  Qwen3 8B, 16K context            grader tier
#   bge-m3         1024-dimension embeddings        embedding tier
#
# Measured on 11 October 2026; see docs/live-session.md.
set -euo pipefail
ollama=${OLLAMA_URL:-http://localhost:11434}

pull() { curl -fsS "$ollama/api/pull" -d "{\"model\":\"$1\",\"stream\":false}" >/dev/null; echo "pulled $1"; }
alias_of() {
  # The bounded live context fits in 16K; the 64K default holds GPU memory
  # the other models need.
  curl -fsS "$ollama/api/create" -d "{\"model\":\"$2\",\"from\":\"$1\",\"parameters\":{\"num_ctx\":16384},\"stream\":false}" >/dev/null
  reply=$(curl -fsS "$ollama/v1/chat/completions" -d "{\"model\":\"$2\",\"messages\":[{\"role\":\"user\",\"content\":\"Return only JSON {\\\"ready\\\":true}\"}],\"max_tokens\":40,\"temperature\":0,\"reasoning_effort\":\"none\",\"response_format\":{\"type\":\"json_object\"}}")
  echo "$reply" | grep -q '\\"ready\\": *true' || { echo "$2 did not return the JSON check: $reply" >&2; exit 1; }
  echo "prepared $2"
}

pull qwen3:4b-instruct-2507-q4_K_M
pull qwen3:8b
pull bge-m3
alias_of qwen3:4b-instruct-2507-q4_K_M qwen3-live
alias_of qwen3:8b qwen3-8b-live
curl -fsS "$ollama/v1/embeddings" -d '{"model":"bge-m3","input":["ready"]}' | grep -q '"embedding"' || { echo "bge-m3 returned no embedding" >&2; exit 1; }
echo "prepared bge-m3"
cat <<EOF

Now bind the tiers in the app, at /admin/models:
  provider  http://host.docker.internal:11434/v1
  monitor, judge  -> qwen3-live      reasoning none
  grader          -> qwen3-8b-live   reasoning none
  embedding       -> bge-m3
EOF
