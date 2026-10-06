# ADR 0002: Use WebSocket for live audio in V1, not WebRTC

**Status:** Accepted
**Date:** 2026-10-06

## Context

The oral round needs sub-1200 ms round trips with barge-in. WebRTC is the
textbook answer and brings ICE, STUN, TURN, an SFU and permanent operational
overhead.

## Decision

Binary WebSocket carrying 20 ms PCM16 frames with a sequence header, behind a
`MediaTransport` interface.

## Consequences

Roughly 100-200 ms worse on lossy networks, which campus desktop users will not
notice. Revisit at V3 when mobile traffic appears; LiveKit slots in behind the
same interface.
