# ADR 0003: Carry `organization_id` from the first migration

**Status:** Accepted
**Date:** 2026-10-06

## Context

Multi-tenancy was proposed as a later addition. Retrofitting tenant columns
into a populated schema means months of migration work and a credible chance of
a cross-tenant data leak.

## Decision

`organization_id NOT NULL` on every tenant-scoped table from migration 0001.
Indexes lead with it. Enforced in the repository layer with Postgres RLS as a
second line. No subdomains, no per-tenant databases, no branding in V1.

## Consequences

Costs a few days now. Saves months later and removes the single most expensive
rewrite on the list.
