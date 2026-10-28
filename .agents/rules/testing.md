# Testing and coverage

For the approved first community publication, only a clean, genuinely parentless
initial commit in a non-shallow repository may establish the coverage baseline.
Full backend branch and frontend branch thresholds and all required regressions
still apply. Record incremental coverage as NOT_APPLICABLE (establishing baseline),
with no invented percentage. Later commits retain both incremental thresholds;
missing comparison history fails and must be fetched. See `scripts/coverage/baseline.mjs`.

Use coverage to identify untested behavior, not as proof of correctness. The owner replaced the former exhaustive coverage campaign on 2026-09-14 with a risk-based policy. [coverage.json](coverage.json) is the only machine-readable threshold authority. Threshold changes must be explicit, reviewed policy changes; never adjust them merely to make a failing task green.

## Admission and scope

- Backend service branches: at least **80%** across the configured service source scope.
- Frontend branches: at least **50%**, checked separately for frontend work.
- Added or modified executable statements and affected branches: at least **90%** each, independently of aggregate coverage.
- These are NSC engineering floors, not universal industry standards or a requirement to exhaust every execution path. Do not continue a coverage-only campaign after the agreed task is complete.

Current automated admission remains backend-service only. It includes internal runtime/business packages plus Controller and Agent entrypoints, including runtime manifest validation, security, configuration and storage. Frontend, scripts, harness/coverage tools and the standalone market publisher are outside backend admission. Helper changes still require applicable correctness tests. Never extend NSC work to neighboring projects.

## Behavior before percentages

1. For changed behavior, test the success path, expected failure and applicable boundaries with observable assertions on results, preserved resources and state transitions.
2. Critical changes require explicit scenarios for authorization and ownership, secret handling, draft versus published state, partial publication, unknown or stale evidence, concurrent/stale operations, cancellation, recovery, certificate renewal and safe exit as applicable. A high aggregate percentage cannot excuse a missing critical scenario. Record which scenarios apply and the evidence in the change description.
3. Add a regression test for a reproduced business defect. Test realistic dependency failures where they affect user-visible state or data safety. Do not mechanically inject every internal SQL/read/write error or enumerate every if/else solely to raise a number.
4. Prefer stable public contracts and isolated fixtures over private call ordering, timing sleeps and tests mirroring implementation. Review or simplify redundant and brittle tests; retain effective behavior protection. Never add assertion-free tests or change production behavior just to make it easier to cover.
5. Review remaining uncovered changed code for business risk, even above the floor. Explain significant coverage drops and any deliberately untested low-risk behavior in the change description. This is a review requirement, not an implemented numeric regression gate or a silent exemption for critical paths.

## Evidence and enforcement

Include unexecuted source. Official generated output, declarations, test source and fixtures may be excluded with reviewable reasons. Do not omit business directories, add coverage-ignore directives or remove assertions to pass. Out-of-scope helpers are reported as outside admission, never as covered. Missing measurement adapters for in-scope executable files fail and list those files.

Native Go statement coverage is not branch coverage. See [measurement limitations](../../docs/test-coverage.md). Changed executable blocks and affected decisions are measured against the Git baseline; pure deletions still require applicable regression tests. Local comparisons include staged, unstaged and untracked files. Missing baselines, failed tests, missing reports or stale source snapshots fail independently of percentages. Report actual covered/total counts and report locations; preserve historical failures as historical facts.

Run `make test-coverage-backend` and applicable harness checks for backend work; use `make frontend-check` for frontend work. Applicable race, database, NGINX, Compose/Swarm and browser checks remain separate requirements. Use isolated NSC fixtures, never live business resources. A percentage does not replace integration or business acceptance evidence.
