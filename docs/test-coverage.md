# NSC testing and coverage

The owner ended the exhaustive coverage campaign on 2026-09-14. Current backend-service admission uses an **80% aggregate branch** floor; the separate frontend branch floor is **50%**, as requested on 2026-10-06. Changed executable statements and affected branches each require **90%**. These are NSC policy choices, not universal industry standards. [Testing rules](../.agents/rules/testing.md) define mandatory business scenarios; [coverage.json](../.agents/rules/coverage.json) is the numeric authority.

This policy follows the risk-based approach in [Google's Code Coverage Best Practices](https://testing.googleblog.com/2020/08/code-coverage-best-practices.html) and [Martin Fowler's Test Coverage](https://martinfowler.com/bliki/TestCoverage.html): inspect uncovered behavior and assertion quality instead of chasing a perfect percentage. Their general guidance does not establish an equivalent threshold for NSC's particular Go decision instrumenter.

Authorization, ownership, publication, unknown evidence, concurrent operations, recovery and exit protections still require explicit applicable behavior tests. Review significant drops and uncovered changed business logic. Keep useful existing coverage; do not manufacture gaps to reduce maintenance or remove tests merely because the floor is lower. Historical runs retain the thresholds and failures applicable when they ran. A policy update is not a new test run or a release acceptance result.

## Run and inspect

```sh
make frontend-install
make harness-check
make harness
# Independent frontend admission:
make frontend-check
# Coverage alone (builds the embedded frontend first):
make test-coverage-backend
```

The backend gate requires Linux `unshare` and `ip` with permission to create a user/network namespace. It brings up only the private loopback interface; Agent validation tests bind their fixed ports there, never on the host. Missing isolation fails verification. Dependencies must already be cached because the coverage namespace has no external network. With dependencies and embedded assets already built, `bash scripts/check-coverage.sh` runs coverage directly. Use isolated NSC fixtures, never live business configuration or neighboring projects. Aggregate backend and incremental thresholds are independent; weighted averages cannot conceal a failure. Failed tests, missing evidence or insufficient coverage return nonzero. Independent reports are produced even after threshold failures.

Harness evidence lives in `coverage/harness/<run-id>/summary.json` and per-step logs. Reports contain source fingerprints, comparison base, commands, timestamps, exit statuses and blocked dependencies. A full run retains copies of available coverage evidence under its `evidence/` directory. It does not claim that separately required race/E2E checks ran.

The harness gives preparation, instrumented branches, native statements and final
admission separate 20-minute step limits. Both Go measurements use explicit
15-minute per-package test deadlines. Statement measurement and final reporting
continue after a branch failure; a failed preparation blocks dependent phases.
`make test-coverage-backend` and `bash scripts/check-coverage.sh` still execute all
four phases as one complete admission gate. Individual phases are available as
`prepare`, `branches`, `statements` and `report`; they share the original source
snapshot and reject source or comparison-base drift before and after measurement.
Preparation clears previous evidence. Each measurement records failure before it
starts, so cancellation or a timeout cannot reuse an earlier success. Reporting
requires successful branch and native measurements as well as incremental
thresholds; successful tests alone do not waive the aggregate branch floor.

## Measurement and limitations

| Report | Scope | Measurement |
|---|---|---|
| `coverage/go-branches.json` | The backend include list in coverage.json, including unexecuted files | Go AST decisions: if, conditional for, range, switch, type switch, select and runtime short-circuit operands |
| `coverage/go-branches-passed.json` | The current branch measurement | Actual instrumenter exit outcome, including test and aggregate-threshold failures |
| `frontend/coverage/coverage-summary.json` | frontend/src/**/*.{ts,vue}, including unimported components | Istanbul branch coverage |
| `frontend/coverage/index.html` | Same frontend scope | Per-file uncovered branches |
| `coverage/go-statements.out` | Native Go source instrumentation | Executable statement blocks for incremental checking |
| `coverage/incremental.json` | Added/modified code relative to Git baseline | Affected statement/branch coverage and unsupported source blockers |
| `coverage/source-snapshot.json` | Change scope and source hashes before testing | Reject stale reports |

Native `go test -cover` measures statements, not branches. The Go decision tool uses a temporary overlay without rewriting source. Implicit switch defaults count; multiple labels in one case clause share an execution arm.

Range measurements distinguish iteration from normal exhaustion; break does not count as exhaustion. Runtime short-circuit operands are measured only when evaluated. Constant operands are not runtime decisions. This report does **not** measure MC/DC or machine-code branches. It is a limited decision metric, not complete control-flow coverage. The instrumenter has fixtures for true/false paths, implicit default, untested files, failed tests, threshold rejection and source preservation.

Official generated files, test source and test/testdata/enginetest/fixtures directories are excluded from Go runtime statistics. The in-memory Engine in internal/runtime/engine/enginetest is referenced only by tests; runtime Engine implementations remain included. Frontend exclusions cover generated declarations, generated messages and test helpers. Build/publishing scripts, harness scripts, coverage tools, the standalone market publisher and frontend are outside the current backend admission scope. They are listed as outside scope, never as covered. Runtime manifest validation, security, configuration and storage remain backend business dependencies. Unsupported executable files inside the backend directories still fail incremental verification. Coverage-tool behavior tests remain necessary to validate measurement. Frontend reports listed above are independent historical or frontend-work evidence; the backend gate does not regenerate or copy them.

## Incremental baseline and evidence

Local comparison defaults to HEAD and includes staged, unstaged and untracked source. Set NSC_COVERAGE_BASE to compare a development branch against another commit. PR CI uses the target merge-base; push CI uses the preceding SHA. An all-zero initial-push SHA compares against an empty repository. CI fetches full history; missing baselines fail.

Changed executable blocks and decisions are the units of measurement. Editing a decision's body requires its affected arms to be covered. Pure deletion adds no executable statements but still requires regression testing. Go uses native coverage blocks and frontend uses Istanbul source locations; neither claims character- or instruction-level precision.

The incremental checker's temporary Git fixtures cover new/staged files, deletion, rename, missing reports, uncovered branches and stale snapshots. They run before the gate. Changed source or comparison base after testing invalidates all reports.

Overlay tests disable built-in vet because probe directories are virtual. Run `go vet ./...` separately. Coverage does not replace race detection, real NGINX, Compose/Swarm, database contracts or browser E2E checks.

## Existing boundary suites

- API route authorization for anonymous/global-viewer writes.
- Gateway stale/out-of-order evidence, unconfirmed loading, restart risk and issue acknowledgement/recurrence.
- External application lifecycle controls, read-only bindings and withdrawn market installation.
- Recovery re-entry protection, idempotent retry, data-boundary acknowledgement and failed refresh.
- Strict manifest parsing, paths, locale declarations, hashes, shared-file cache, secrets and file/size limits.
- Publication failure retaining existing entrypoints; symlink, oversized-file and invalid Git-source rejection.
- Missing document bodies, fallback, document identity, untrusted links and embedded images.
- Harness contracts, immutable artifacts, missing tools, failed prerequisites, timeout, cancellation and source drift.

## Future changes

Run focused behavior tests first, then the applicable gate. Prioritize meaningful risk and regression coverage rather than an exhaustive branch-by-branch campaign. Record critical scenarios, remaining risks, command outcomes and fresh report locations. The backend-only gate does not certify frontend, helper tools or production deployment. Uncovered entries remain visible even when percentages pass; unsupported source and failed tests continue to block the gate.
