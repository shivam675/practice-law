# Teacher authoring completion report

## Owned files

- `apps/web/src/routes/Templates.tsx`: create template, edit as new immutable version, publish draft version, permission-aware actions and visible load/mutation errors.
- `apps/web/src/routes/TemplateAuthor.tsx`: native authoring form for all five stage kinds, add/remove/reorder, rubric choice, participation and AI actors loaded from `/ai-profiles`. Existing configs, participation properties and defaults survive untouched edits. One actor is presiding when actors exist; removing the presiding actor transfers the role to the first remaining actor. API validation reports all template problems.
- `apps/web/src/routes/Rubrics.tsx`: resumed existing create/edit-as-copy implementation; fixed strict TypeScript error. Copies clear the rubric key and omit database IDs, preserving existing assessment criteria.
- `apps/web/src/routes/AssessmentDetail.tsx`, `AssessmentResources.tsx`: material titles/kinds/visibility, native upload with 25 MB client check, visible errors and completion status.
- `apps/api/internal/submissions/resources.go`: resumed original handlers, gofmt, Unicode-aware title validation. Tenant assessment validation precedes multipart parsing; file limit, sniff, extraction, truncated rejection, original blob and parsed document link, and audit record remain implemented. Teacher listing requires knowledge.view and assessment.view and returns metadata only.
- `apps/api/internal/submissions/resources_test.go`: supported kind/visibility matrix, actual template sides, blank/unknown fields and title boundary tests (including multibyte titles).

## Exact shared wiring (coordinator-owned)

In `apps/api/cmd/api/router.go`, inside existing `/assessments` route:

```go
r.With(authz.Require("knowledge.view")).Get("/{assessmentID}/resources", submissionHandlers.ListResources)
r.With(authz.Require("knowledge.upload")).Post("/{assessmentID}/resources", submissionHandlers.UploadResource)
```

`submissionHandlers` is already instantiated. No new store or migrations needed.

In `apps/web/src/App.tsx`:

```tsx
import { Rubrics } from "./routes/Rubrics";
// Inside the existing AppShell route, adjacent to Templates:
<Route path="/rubrics" element={<Rubrics />} />
```

In `apps/web/src/app/AppShell.tsx`, next to the Templates navigation item (Stack is already imported):

```tsx
{ to: "/rubrics", label: "Rubrics", icon: Stack, permission: "rubric.view" },
```

Assessment detail panel is already connected inside the owned file. Templates uses existing APIs and route.

## Checks

- `docker compose exec -T api go test ./...`: passed after final resource changes.
- `docker compose exec -T web npm run build`: passed; final rebuild also run after list-field editing refinements.
- Existing student materials code inspected: side/stage filtering present, but a reserved-side edge case was reported to the coordinator (below).
- No commits, real-record mutation smoke, dependency additions or schema changes.

## Integration concern and limits

Existing `apps/api/internal/assessments/resources.go` selects `ks.visibility IN ('all', $3)`. Participation currently permits a side named `staff`, which can match staff-only materials. Coordinator must add `AND ks.visibility <> 'staff'` to the student query. Also add `d.organization_id = ks.organization_id` to its document subquery for explicit tenant scope. These changes are outside this agent's assigned files and were sent to the coordinator.

Browser mutation smoke remains with the coordinator after router integration. The resource validation unit tests do not prove tenant/permission HTTP integration or extraction failure behavior.

A failed resource database transaction after blob storage may leave an unreferenced original blob, matching the existing upload approach. No cleanup job was introduced. Template creation uses the existing two-step APIs: if version validation fails, the form retains the newly created template ID so retry does not create a duplicate; cancelling at that point leaves an empty template that can be completed with Add first version.
