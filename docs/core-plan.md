# Core completion

User scope: teacher tools, AI judging, grading, administration; prioritize lower speech latency and missing transcript words. Use existing dependencies, permissions, UI tokens and tenant boundaries. Preserve published assessment definitions and grades. No cloud speech transfer without a chosen provider and explicit configuration. Do not publish real results or change existing users' credentials while testing.

1. Speech: reproduce dropped words, fix capture flush and model contention, preserve full interim text, stream queued voice chunks, enforce bounded judge latency, verify with synthetic audio.
2. Teacher authoring: editable versioned templates/stages and rubrics; upload/list case resources using existing parser and storage. Native accessible forms, no JSON-only editor.
3. AI judging: resolve configured actor profiles, enforce capability/cooldown/quota, use prepared questions and bounded live generation; preserve audit history.
4. Grading: examine the complete record in bounded model calls; permit one-criterion regrading before publication without losing prior results on failure.
5. Administration: reuse team form for safe membership editing; organisation settings/creation for authorized administrators; administrator-assisted password recovery with short-lived one-use links and session revocation.
6. Verify: Go tests, web build, media regressions, isolated core API smoke and browser checks. Review diffs; leave existing assessment results untouched.

Integration: controller owns router.go, App.tsx and AppShell.tsx. Implementers report exact wiring instead of editing these shared files. No implementation agents run concurrently with each other. Speech and grading are controller-owned.
