---
name: nsc-verify
description: Verify an NSC change with the repository harness and interpret its coverage and integration evidence.
---

# nsc-verify

Read [harness](../../rules/harness.md) and [testing](../../rules/testing.md). Inspect the current diff before selecting checks; preserve unrelated edits.

Run make harness-check for a fast contract/static pass and make harness for delivery evidence. The embedded frontend must be built before API tests can use it. Reuse an installed toolchain through GO/NPM/NODE/PROTOC rather than changing pinned versions.

Use the run summary and step logs to distinguish failure from missing prerequisites. Read full and incremental coverage reports separately, including unsupported files and stale-source failures. Select applicable race, store, NGINX, Compose/Swarm and browser tests from docs/development.md using isolated fixtures. Do not substitute a successful build for those tests.

Report executed commands, actual coverage fractions, blockers and evidence paths. Use a fresh run after edits; never relabel historical evidence as a new verification.
