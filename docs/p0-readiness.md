# Core P0 acceptance and evidence

This contract covers trusted internal administrators managing several Compose
and Swarm projects. R01–R08 are the core P0 admission scope. Project RBAC (R09),
a backup scheduler/restore center, automatic database promotion and a complete
notification center remain separate work. OVH-specific tests are **DISABLED**;
this is an exclusion, never a passing provider test.

## Acceptance ledger

| Item | Required behavior | Executable evidence |
| --- | --- | --- |
| R02 DNS credentials | Configuration/DB precedence, private encrypted profiles, authenticated mutation, key rotation and readable history | Core/API DNS profile recovery tests; [credential contract](security.md) |
| R03 remote storage | Fresh immutable node/engine/volume identities, offline/stale destruction refusal, reviewed force exit, preserved data | `NSC_RETENTION_ACCEPTANCE=true python3 scripts/e2e-market-swarm.py`; [remote Worker storage](remote-worker-storage.md) |
| R01 Gateway continuity | Loaded/disk revision tuple, NGINX rejection preserving LKG, serial replacement, control outage, active streams, fault recovery | `python3 scripts/gateway_acceptance.py --verify-lb-redundancy --output coverage/p0/gateway/summary.json`; [Gateway acceptance](gateway-acceptance.md) |
| R04 Controller/DB failover | Exclusive owner, crash/partition exclusion, planned real replica promotion, uncertain Docker acceptance and stale-attempt fences | `scripts/e2e-ha.sh postgres` and `scripts/e2e-ha.sh mysql`; deployment ownership regressions; [failover acceptance](ha-failover-acceptance.md) |
| R05 layered HA | Independent ingress processes, alert firing/resolution, one/two Manager loss and preserved quorum state | Gateway `--verify-lb-redundancy`; `make test-ha-alerts`; `python3 scripts/test-swarm-quorum.py --output coverage/p0/quorum.json`; [HA blueprint](ha-blueprint.md) |
| R07 protected recovery | Native backup/restore in all stores, private inspection without writes, complete keys, held jobs, reviewed fresh runtime reconciliation | `python3 scripts/e2e-recovery.py --image nsc-controller:candidate --output coverage/p0/recovery`; `python3 scripts/e2e-recovery-reconciliation.py --image nsc-controller:candidate --output coverage/p0/reconciliation`; [recovery](recovery.md) |
| R06 layered readiness | Accepted/workload/certificate/Gateway/client layers, stale/partial UNKNOWN, read-only real client DNS/TLS/HTTP checks | Core/API/readiness UI and browser tests; `python3 scripts/test-verify-access.py`; [operation readiness](deployment-readiness.md) |
| R08 admission | Current source/contracts/images and executed required results; absent, stale, skipped, failed or cancelled evidence blocks | `make test-readiness-gate`; `scripts/e2e-installation.py` for supported profiles; CI `readiness` job; Release reuses the complete CI workflow |

Run current-image tests, not an older successful candidate. Privileged nested
Docker fixtures use only owned resources; a same-host fixture does not establish
independent production failure domains. Retention tests do not restore business
data, and Gateway tests do not establish UDP/HTTP3 rollout continuity or any
particular production load balancer's behavior.

## Evidence and admission

`.github/readiness-policy.json` lists mandatory CI jobs and case groups. Each job
captures actual step outcomes in a minimal envelope. The final gate requires
all jobs, their required checks and current-run artifacts. It rejects missing,
duplicate, unsuccessful and incompatible evidence; cancelled or skipped jobs
never satisfy admission. Gate regression tests include those negative cases.

Every envelope records the Git commit, SHA-256 source fingerprint, Gateway
protocol, renderer contract and ordered migration digests for all stores.
Attempt-specific artifact names and download patterns prevent old results or
immutable artifact name conflicts during reruns. Rerun **all jobs** to obtain
a complete current-attempt admission; partial reruns retain missing evidence
as a failure. Locked fixture image preparation works without a prewarmed Docker
cache and canonical aliases support nested Docker save/load.

Container evidence also requires immutable OCI image-config digests and matching
source labels. A local image-config digest identifies the tested image contents;
it is not the registry manifest digest. Release records the latter separately
from the publishing action's output.

CI runs full backend admission, frontend warm and clean type checks, race/fuzz,
external-store contracts, real NGINX, Compose/Swarm/ACME and isolated P0 fixtures.
A successful harness alone is insufficient. Release calls that complete workflow
before preparing/publishing assets, validates the tag against `VERSION`, and
creates the GitHub release only after both architecture archives and both image
jobs finish. Release builds one multi-architecture candidate, scans both immutable child
digests, and promotes the same index only after both scans pass. Versioned images
retain source labels, provenance, SBOM and published manifest digests; stable aliases require an explicit later promotion.

Only minimal verification envelopes and build artifacts are uploaded by the new
acceptance jobs. Recovery source directories contain backups, private keys and
configuration: never upload them, request logs or client token files. Local
fixture reports stay in ignored private `coverage/` paths. A local passing report
does not prove that the GitHub run passed, that production was deployed, or that
an external provider accepted an operation.

## Upgrade and rollback matrix

| Boundary | Supported acceptance | Unsupported inference |
| --- | --- | --- |
| Controller ↔ Gateway | Matched candidate source, protocol and renderer; current loaded/disk tuple | Mixing unknown protocol or renderer versions because a TCP connection succeeds |
| Controller ↔ store | Explicit migration with backup, all peers using the matching schema and one writer | Downgrading an evolved schema by only restarting an old image |
| Gateway restart | Persistent per-Gateway identity/generation state; real cold-start LKG test | Sharing one mutable state directory between different Gateways |
| Application rollback | Immutable Compose revision and reviewed runtime/storage identity | Reversing business data/schema migration or recreating a missing volume |
| Protected restore | Isolated native archive restore, complete keys/trust, held pending work, reviewed release | Restored historical Gateway ACKs or interrupted tasks proving current success |

Before upgrades, record the currently admitted Controller/Gateway image digests,
protocol/renderer/migration tuple and backup checksum. Validate the candidate in
an isolated restore, then roll Gateways with explicit drain and loaded-version
checks. Keep the previous verified images and data backup. A schema downgrade
requires a separately tested compatible restore; NSC does not infer it from a
configuration rollback. Preserve rejected candidate and failed-operation history.
