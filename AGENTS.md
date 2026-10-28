# NSC repository instructions

These instructions apply to network-service-composer and its subdirectories only. Preserve unrelated working-tree changes and do not extend development or verification to neighboring projects. Higher-priority agent instructions and explicit user instructions take precedence over repository guidance; existing authorization remains effective within its scope.

Before development, read the mandatory rules:

- [Workflow and management boundaries](.agents/rules/workflow.md)
- [Engineering harness](.agents/rules/harness.md)
- [Testing and coverage](.agents/rules/testing.md), with [machine thresholds](.agents/rules/coverage.json)
- [Internationalization](.agents/rules/i18n.md)
- [Code conventions](.agents/rules/code-style.md)
- [English documentation and historical exceptions](.agents/rules/documentation.md)

Project skills live in [.agents/skills](.agents/skills). Use the relevant skill for verification, boundary tests or i18n work; rules are linked explicitly and are not assumed to auto-load.

For backend service changes, run `make test-coverage-backend` and applicable verification; frontend and auxiliary tooling are outside backend admission. For broader changes, run `make harness` and applicable integration checks before delivery; use `make frontend-check` for frontend coverage. Read thresholds from the linked coverage policy. Prioritize business behavior and critical failure boundaries; do not mechanically exhaust every if/else or pursue full coverage after the agreed task is complete. Keep failing tests and missing evidence visible, preserve useful existing tests, and never hide untested source or confuse statement and branch coverage. Policy changes require explicit authorization and review.

The project is in development. Authorized breaking changes are allowed; do not advance the product version unless the user requests it.
