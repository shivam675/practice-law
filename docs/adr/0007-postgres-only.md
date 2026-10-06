# ADR 0007: One database: Postgres with pgvector, no dedicated vector store

**Status:** Accepted
**Date:** 2026-10-06

## Context

Retrieval corpora are small: one moot problem, a handful of statutes and twenty
or so authorities per assessment. A dedicated vector database would add an
operational component with nothing to show for it.

## Decision

pgvector with HNSW for dense retrieval, `tsvector` for lexical, fused with
reciprocal rank fusion. Jobs, queue state, embeddings and business data all
live in the same Postgres instance.

## Consequences

Transactional enqueue comes free, there is one thing to back up, and one thing
to operate. Revisit only if a single corpus exceeds a few million chunks.
