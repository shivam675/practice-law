# Teacher authoring task

Implement task 2 from the authorized core scope in X:/megamoot. Read CLAUDE.md and existing touched files. Ponytail full: reuse code, native forms, no new dependencies. Do not spawn agents. Do not commit changes.

Own only teacher UI files (Templates.tsx and new authoring components, a Rubrics.tsx route, AssessmentDetail.tsx resource panel) and teacher-resource backend (prefer new internal/submissions/resources.go reusing Store.blobs, Store.ai, sniff, safeFilename, contentTypeFor). Add minimal meaningful tests or isolated smoke script. Do not edit router.go, App.tsx, AppShell.tsx: return exact wiring instructions. Do not edit speech, sessions, grading, reports, users, teams or orgs.

Requirements:
- Create templates, edit by creating immutable versions, choose existing rubric, add/remove/reorder stages and edit all five supported kinds with accessible native fields. Preserve untouched existing configs/participation/defaults when editing. Support AI actor selection using /ai-profiles; participation requires one presiding actor when actors exist. Existing template APIs already validate specs.
- Rubric create and edit-as-copy (new key and rubric, never mutate criteria already used by assessments). Criterion name, key, weight, max, description/guidance. Reuse existing /rubrics endpoints.
- Case-material upload and list scoped to /assessments/{assessmentID}/resources. GET permission knowledge.view, POST knowledge.upload. Validate tenant assessment before parsing; cap 25 MB; validate format using existing sniff, use extraction service, reject truncated parsing instead of silently accepting. Keep original blob and parsed document linked to knowledge_sources. Kind and visibility must validate against supported kinds and actual template participation sides plus all/staff. Do not expose staff-only contents to students. Preserve audit log.
- Native resource form on assessment details, no implementation jargon in UI. List existing resource titles/kind/visibility.
- Preserve existing tokens and components. Show mutation errors; no silent failures.

Tools: Docker is at C:/Users/bolub/AppData/Local/Programs/DockerDesktop/resources/bin/docker.exe, requires require_escalated. Containers running. Web build: docker compose exec -T web npm run build. Go test: docker compose exec -T api go test ./... . Avoid running smoke that publishes grades or changes real records; use unique test-only records if needed.

Write completion report to docs/teacher-report.md with owned files, exact router/UI wiring, checks and any concerns. Final response concise.
