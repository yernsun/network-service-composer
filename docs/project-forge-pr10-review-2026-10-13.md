# Project Forge PR 10 compatibility review

This review records the 2026-10-13 Linux verification of Project Forge's frontend
template dependency update and retains the patches needed before using that
template as a generator baseline. The patched generator passed installation,
type checking, generated-project tests, OpenAPI verification and isolated Compose
checks. The original main branch also failed current dependency audits, so its
historical green CI result does not establish a current clean audit baseline.

The patches target Project Forge. This NSC commit archives engineering evidence;
it does not change NSC runtime dependencies or apply the patches to NSC's imported
frontend. Project Forge PR 10 remained open and unchanged at review time.

## Reviewed revisions

- [Project Forge PR 10](https://github.com/yernsun/project-forge/pull/10), head
  `99eb3e96087f30e932323854afcf0af5c6a81398`.
- Main baseline `2d06880fbec05e9abe82724a59c6dd30142e9953`.
- [Failed PR CI run](https://github.com/yernsun/project-forge/actions/runs/37848142992).
- [Earlier successful main CI run](https://github.com/yernsun/project-forge/actions/runs/34759510868).

The original PR changes only the frontend template's package manifest and lock
file, with 23 direct dependency updates. All 16 failed jobs were inspected.

## Failure causes

| Cause | Failed jobs | Evidence and resolution |
| --- | --- | --- |
| TypeScript peer conflict | 9 | TypeScript 7.0.2 conflicts with openapi-typescript 7.13.0's TypeScript 5 peer range. typescript-eslint 8.71.1 also excludes TypeScript 7. Restore TypeScript 5.9.3. |
| Node type contract assertion | 6 | Quality, Python compatibility and platform jobs fail the same assertion requiring exactly `^22.12.0`. Keep Node types in the 22 series and check manifest/lock consistency rather than a fixed patch floor. |
| Python audit baseline | 1 | urllib3 2.7.0 has three reported vulnerabilities, with 2.8.0 listed as the fix. The same main lock independently reproduces this failure. |

The unpublished `project-forge 0.3.0` package appears under the audit's **Skip
Reason**; it did not cause the failing exit code. The historical main run already
had that skip and exited successfully. The reported urllib3 IDs are
`PYSEC-2026-4177`, `PYSEC-2026-4176` and `PYSEC-2026-4175`.

After resolving installation, local checks exposed further compatibility issues:

- jsdom 30.1.2 requires later Node 22/24 minor versions than the declared
  `>=22.13 <23 || >=24 <25` range. Restore jsdom 27.4.0.
- Vue Test Utils 2.5.1 introduces js-beautify 2, nopt 10 and abbrev 5, which also
  raise the Node floor. Retain Vue Test Utils 2.4.11 with a tilde range.
- PrimeVue 5.0.2 requires a numeric `ProgressSpinner.strokeWidth`. Authentication
  templates failed with TS2322 until `stroke-width="5"` became
  `:stroke-width="5"`.

The unchanged main frontend lock also produced audit reports for nine affected
packages: two critical, four high and three moderate. These are affected-package
counts, not counts of distinct vulnerabilities. Retained Vue/Vitest upgrades and
compatible brace-expansion/postcss-selector-parser patches eliminated the final
frontend audit reports.

## Applicable patches

- [Frontend compatibility patch](../contrib/project-forge/pr10/frontend-pr10.patch):
  TypeScript `~5.9.3`, Node types `^22.20.1` (locked at 22.20.5), jsdom `^27.4.0`,
  Vue Test Utils `~2.4.11`, a normally regenerated npm lock, the spinner fix and
  contract test updates. CI adds Node 22.13.0 and 24.0.0 alongside current Node
  22/24 checks. Other verified upgrades remain.
- [Independent Python baseline patch](../contrib/project-forge/pr10/python-baseline.patch):
  upgrade only urllib3 from 2.7.0 to 2.8.0 in the root and backend template locks.
  Dependency declarations, product versions and audit policy remain unchanged.

Both patches were actually applied to an isolated worktree at the reviewed PR
head, followed by a successful complete generator harness. The Python patch also
passed an application check against the reviewed main baseline.

From a clean Project Forge worktree at the reviewed head, with the downloaded
patch files in the current directory:

```sh
git apply --check frontend-pr10.patch
git apply frontend-pr10.patch
git apply --check python-baseline.patch
git apply python-baseline.patch
uv sync --all-groups --frozen
uv run --frozen python harness/check.py
```

The original PR was not pushed or merged. Local results do not replace CI on a
subsequently updated remote branch.

## Executed verification

| Check | Recorded result |
| --- | --- |
| Node 22.13.0 and 24.0.0 | Normal `npm ci`, lint, type checks, coverage tests and builds pass for core, sample, auth and auth-sample frontends; respectively 5, 9, 31 and 35 unit tests per runtime. |
| Locked Node engines | All 211 engine declarations cover the complete declared Node range. |
| Generator harness | 194 passed, 2 platform skips; includes all 36 valid profile, feature and locale combinations, ruff, mypy, skill validation and the coverage gate. |
| Combined applied patches | The complete generator harness passes from the original PR head after applying both patches. |
| OpenAPI | All four real backend contracts and generated TypeScript declarations match tracked artifacts. |
| Generated projects | Both frontend profiles and backend-minimal, fullstack-auth-sample and fullstack-auth-evented pass their applicable harnesses. Dedicated PostgreSQL databases support migration and backend tests: 40, 104 and 103 passed, each with one platform skip. |
| Audits | Final frontend npm audit and patched root/backend pip-audit exit zero with no vulnerability reports. Local unpublished packages remain normal skips. |
| Production Compose | Configuration, image build, startup, readiness and API/event-relay logging checks pass. |
| Development Compose | Startup, readiness and API logging pass; Chromium tests for shell loading and signup, workspace item, session restoration and logout report 2 passed. |
| Cleanup | Task-owned containers, networks and volumes were removed; the dedicated PostgreSQL fixture was also removed. |

The platform skips are Windows junction cases on Linux. This review does not
claim fresh macOS/Windows execution or the full Python-version matrix. Coverage
gates passed; the pytest combined coverage percentages are not independent branch
coverage percentages.

The initial generator check failed after the expanded CI matrix exposed another
fixed matrix assertion; the assertion was updated and the full harness rerun.
The initial authenticated-project checks failed at the spinner type mismatch.
Those complete local logs were superseded, but the observed TS2322 excerpts were
retained. The first development browser preparation failed with EACCES after a
container-created mount directory; a separate browser fixture resolved it and
the complete flow was rerun. Historical failures remain distinct from final passes.

## Retained evidence

- [Execution records](../contrib/project-forge/pr10/verification-results.json)
  retain timestamps, commands, exit codes, durations and original log hashes,
  including intermediate failures. Temporary root paths are normalized for
  publication and labeled explicitly.
- [CI failure groups](../contrib/project-forge/pr10/ci-failure-summary.json)
  retain the original job IDs and failed steps.
- [Node range check](../contrib/project-forge/pr10/lock-engine-range-validation.json)
  records the 211 checked engine declarations and empty invalid set.
- [Provenance and checksums](../contrib/project-forge/pr10/provenance.json)
  bind the public patches and execution records to the original evidence archive.

Raw runtime logs and the original evidence archive remain in ignored local
coverage storage under the repository privacy rules. The committed report and
execution records describe historical test results, not an NSC deployment or
current acceptance claim.
