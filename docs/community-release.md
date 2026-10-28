# v0.1.0 community release preflight

This is a **release candidate**, not a publication or production-deployment
claim. The target repository is
[network-service-composer](https://github.com/yernsun/network-service-composer).
The candidate uses a new initial Git commit, Apache-2.0, and the existing Go module
path. Database migration numbers and protocol versions are not reset. Historical
records retain their original dates, versions and results.

## Product contract and admission

The scope is trusted-team, multi-project Compose/Swarm operations. Administrators
have host-level deployment authority; global viewers are not separate tenants.
Project automation keys have explicit scopes. Both project and legacy environment
deployment keys cannot use host mounts or globally named/external volumes.
Kubernetes, billing, arbitrary tenant sandboxing
and a complete business-data backup center are unimplemented.

The following capabilities are **experimental in this candidate until the
source-bound release evidence passes**. This classification does not waive any
required test or confidentiality/data-safety requirement. Formal support requires
successful core flows, required CI jobs, final-artifact inspection and resolved
licensing/provenance. OVH provider verification is **DISABLED**; runtime provider
code remains present. Real DNS provider acceptance is separate from Pebble ACME.

| Capability and supported conditions | Contract and failure boundary | Regression evidence required |
| --- | --- | --- |
| Installation and identity | Fresh administrator initialization, login/logout, CSRF, viewer write denial, revoked sessions; failed install must preserve recovery options | API authentication/session tests; isolated installation and browser fixtures |
| Projects and environments | Scoped ownership, ETag conflicts, immutable review target, variable/secret layers, no cross-environment mutation | Core environment/project tests; project-key API tests; workspace lifecycle/browser checks |
| Application lifecycle | Image/Compose/catalog creation, draft recovery, preflight, review, queued deployment, update, stop/start and delete; submitted does not mean running | Core application/service-review suites; worker tests; real Compose/Swarm and workspace wizard |
| Compose and Swarm execution | Fresh manager capability, node identity, lease/attempt fencing, bounded execution, idempotency; unknown outcomes require reconciliation before replay | Cluster/worker/command boundary suites; HA/partition and Swarm fixtures |
| Resource ownership and storage | External resources stay external; collisions block; volume retention and selected data node survive updates; no destructive cleanup on another host | Ownership/adoption/retention suites; scoped storage tests; isolated retention/recovery |
| Domains and gateways | Conflict detection, preview, validated generations, partial failure/rollback, ACK and loaded revision evidence; real HTTP/TLS access | Gateway ingress and release tests; real NGINX TLS, IP access and authority fixtures; delivery E2E |
| Certificates | Import/ACME/renewal, exact domain coverage, binding/list filters and refresh; self-signed P-256 defaults to 20 calendar years (1–100 configurable) and needs explicit client trust | Core/API self-signed tests; eight-locale browser flow; Pebble plus separately identified real-provider evidence |
| Image updates | Digest observation, policy authorization, duplicate suppression, ignore/retry and deployment association; insufficient baseline blocks auto-apply | Image-policy and worker suites; workspace image-update fixture |
| Notifications and activity | Durable event, HMAC webhook, routing, retries, deduplication, mute/recovery and delivery records; public HTTPS-only destinations | Notification/storage suites; DNS/literal-address boundary tests; workspace notifications |
| HA and recovery | Shared SQL backend, leader takeover, stale executor isolation, database failure and recovery barrier; config recovery does not restore business data | Three-dialect conformance; race; HA/failover; retention and recovery acceptance |
| Console | Unique active menu, fresh deep links and post-save updates, stale response rejection, preserved drafts, responsive layout, eight locales | Frontend strict checks/coverage; workspace/shared/demo and real-API browser scenarios |

## Security and trust changes

The pre-fix security review recorded four issues: cross-pool probe writes,
unverified HTTPS upstream peers, incomplete special-purpose address filtering,
and scoped deployment-key host-storage authority. The candidate adds identity
binding, verified upstream TLS with configurable CA trust, a common outbound
address policy, and a persisted storage restriction checked at review/execution.
Regression evidence must validate these changes; review coverage remains partial
and must not be described as an exhaustive security proof.

The Agent also waits for its own NGINX PID and initial generation health before
applying a snapshot or activating a sandbox. Process launch alone is not readiness;
cancellation must preserve the previous generation and cannot report success.

The default Compose Controller and Gateway share database authority. Management
HTTPS, network restrictions, backups and key protection are operator duties.
Swarm examples do not provide all Compose container hardening settings. See
[security](security.md) and the [reporting policy](../SECURITY.md).

## Privacy and clean-source export

`node scripts/release/export.mjs <new-directory> <manifest>` exports only listed
source paths, rejects sensitive-file patterns and symlinks, preserves pending
source changes, and creates exactly one local initial commit without old objects,
tags, hooks, remotes or personal Git identity. The output is explicitly unverified
until the release gates pass. Original `.git`, credentials, `.env`, databases,
archives, caches, browser state and raw evidence are not copied.

The local port override is ignored and retained locally. Thirteen historical
installation screenshots contained real project/domain identifiers and were moved
to restricted evidence. Demo screenshots remain historical examples. A historical
rollout narrative was sanitized without changing its date or test outcomes.
The older history contains operational records; a fresh export does not remove
already published copies or revoke credentials. Any confirmed credential exposure
requires a separately tracked owner rotation/revocation action.

Historical fixed viewer credentials were found in a browser fixture that creates
synthetic test accounts. They are public test data, not deployment defaults. Their
use in any persistent environment must be checked by its owner and rotated if
reused. Raw matches and original screenshots remain outside the source export.

Dependency review upgraded the Go toolchain and vulnerable Go/npm dependencies.
Vitest 4 requires explicit module-mock state reset between tests; the shared
test cleanup preserves this isolation without changing coverage scope or floors.

## Acceptance and release gates

Run `make frontend-check`, `make test-coverage-backend`, `make harness-check` and
`make harness`; race, bounded fuzz, three-dialect conformance and capacity benchmarks;
isolated NGINX, Compose, Swarm, ACME, browser, HA, retention/recovery and installation
fixtures; amd64/arm64 builds/startup; vulnerability and license review; final source,
archive, binary, image and metadata inspection. Use only owned synthetic resources.
Missing infrastructure is **BLOCKED**, never PASS or silently skipped.

All evidence must identify the exact candidate fingerprint. Fixes require a new
candidate and affected revalidation. The initial-commit incremental result is
**NOT_APPLICABLE: establishing baseline**; backend branches remain at least 80%,
frontend branches at least 50%, and full regression remains required. Later commits
require at least 90% incremental statements and branches. Missing ordinary history
fails; shallow clones cannot receive the initial-commit exception.

Publication remains **BLOCKED** until every required gate is satisfied, unresolved
third-party licensing/source provenance is closed, and final artifacts have no
unhandled sensitive findings. See [third-party notices](../THIRD_PARTY_NOTICES.md).
The release owner must enable private vulnerability reporting in the new repository.
This task does not push, tag, publish a Release, or modify production deployment.
