# SDD ledger — plan: X:/megamoot/docs/core-plan.md

No separate spec: user selected audited core gaps. Ruling: use bounded existing flows and preserve backend invariants; no new architecture.

| Tasks | Shared interface | Decision |
|---|---|---|
| Speech / judging | /media/question JSON | Keep compatible question string and optional metadata. |
| Teacher / grading | immutable template rubric IDs | Edit definitions as new versions/copies. |
| Teacher / administration | routes and nav | Controller owns wiring. |
| All | tenant/auth conventions | Reuse permissions; no model state transitions. |
| Speech | regression | Failed short-final-buffer check reproduced, now passes. |
| Teacher | forms + API | Existing APIs plus isolated source-material handler. |
| Judging | timeout + fallback | Model failure cannot block student speech. |
| Grading | complete record + regrade | Preserve old results until replacement commits. |
| Administration | recovery | No email provider installed; use explicit admin-issued one-use link. |

Task 2: dispatched teacher authoring. Controller handles speech and grading.
