---
name: nsc-boundary-tests
description: Design NSC behavior tests for ownership, asynchronous deployment, recovery and state-evidence boundaries.
---

# nsc-boundary-tests

Read [testing](../../rules/testing.md) and [workflow](../../rules/workflow.md), then inspect nearby test fixtures and the real state transition.

For write paths, exercise both the API authorization boundary and the executor ownership boundary. For asynchronous operations, test duplicate requests, interruption, stale completion, partial success and unknown results where applicable. For Gateway evidence, distinguish delivery, loading, access verification, expiry and restart safety.

Assert resource state, preserved data, operation history and allowed next actions rather than private implementation calls. Recovery tests must not imply database or volume rollback. Use injected runtime/store failures and temporary fixtures instead of live workloads.

Run focused tests first, inspect uncovered decisions, then use the repository harness. An unsupported coverage adapter remains a failure even if its behavioral tests pass.
