# Practical operations capability ledger

NSC targets trusted internal teams operating multiple Compose and Swarm projects.
This ledger distinguishes source implementation from environment configuration,
deployment and acceptance. A local build, a simulated executor result or a passing
unit test does not prove production availability or recoverability.

## Implemented operations foundation

| Capability | Source behavior | Acceptance boundary |
| --- | --- | --- |
| Notifications | Encrypted signed HTTPS receivers, environment/project/global fallback, shared-scope dedupe, bounded silence, indexed durable retries, collection/queue health and filtered delivery history | Receiver acceptance is not a human acknowledgement; exactly-once network delivery is not guaranteed; no live receiver is configured by development tests |
| Alert evidence | Shared health predicates, durable issue occurrences, responsibility and acknowledgement identity, authoritative multi-environment ownership and complete-inventory deployment-target-loss detection | Unknown or unmonitored evidence cannot establish recovery; the activity hub links stored ownership, acknowledgement, responsibility and related operations; see [alert evidence](alert-routing.md) |
| Environment prerequisites | Read-only management, image-baseline, certificate/ACME, notification and internal HA prerequisites; mutation refresh, session-scoped cache and unknown after failed reads | Configuration and historical successes do not prove current DNS access, external ingress, storage failover or backup health |
| Node execution | Frozen physical task identity, reference-only mTLS execution, term/attempt/session fencing and separate configured/actual location evidence | Compose and volume requests retain the original Engine; uncertain accepted effects require inspection and explicit recovery review; see [execution](operation-execution.md) |
| Manager evidence | Fresh verified identity and quorum for Swarm execution, with same-ClusterID Manager selection | Another cluster cannot grant authority; observation, actual executor and business workload readiness remain separate |
| Image update console | Policy editing, saved-state checks, digest candidates, explicit deployment/retry/ignore confirmation, operation links and one-time CI signing keys | Same-tag digest changes only; managed adoption and a successful pinned baseline are required; existing saved automation choices are preserved |
| Configuration recovery | Existing protected CLI inspection, key/schema checks, held work and explicit reconciliation/release | Does not restore business databases or volumes; no scheduled backup or restore center is implied |

See [environment operations](environments-nginx.md),
[deployment readiness](deployment-readiness.md), [recovery](recovery.md) and
[HA boundaries](ha-blueprint.md) for the supported contracts. Missing or stale
evidence remains unknown. Configuration, execution, loaded-version and client
access evidence must remain distinct.

## Current closure and remaining work

| Workstream | Remaining implementation |
| --- | --- |
| A: environment operation closure | Certificate/ACME, notification and internal HA prerequisites and retained-task guidance are implemented; backup prerequisites depend on the separate backup center. Explicit inspection/release for uncertain standalone config/secret/volume writes remains an operator boundary. |
| C: incident workflow | The activity hub provides persisted-scope/state/resource filters, acknowledgement and responsibility, correlated delivery history and operation links. Routing, silence and delivery diagnostics remain in [delivery operations](notification-delivery.md); unknown accepted writes require review rather than blind replay. |
| D: control-plane backup center | Consistent native backups for every supported store, manual/scheduled jobs, S3-compatible destinations, checksums, retention and isolated restore inspection through the existing recovery barrier |
| E: layered HA acceptance | Live layer evidence and independent monitoring examples are implemented; database replication/failover and external failure domains remain unverified. Current-build fixture results are recorded separately; real DNS-provider issuance/renewal acceptance remains separate. |

Controller replicas do not provide database replication, ingress failover or
business-data protection. Database promotion belongs to an external fenced
single-writer system. Independently operated monitoring must report a complete
NSC outage. OVH-specific verification remains **DISABLED**, never passed.

## Follow-on capability packages

P1 packages remain separate implementation work: project/environment roles,
fixed-commit Git delivery and drift review, notification provider adapters,
correlated diagnostics and capacity trends, consistent application-data backups,
and controlled migration/adoption/exit analysis.

P2 requires separate contracts for preview environments, build services,
cross-application workflows, OIDC/SSO, maintenance windows and batch rollout.
Kubernetes, billing and an NSC-managed database failover operator are outside
this internal Compose/Swarm roadmap.

## Verification and delivery

Each increment needs business-boundary tests, applicable frontend/backend
admission and integration checks. Final delivery requires the complete harness
against fixed source, synchronized API/translations, privacy review and explicit
reporting of missing evidence. Keep real deployment inventories, backup material,
hosts, domains, keys and operational reports outside Git. Do not enable automatic
updates, adopt resources or migrate business data as a side effect of installation.
