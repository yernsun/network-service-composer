# Operating workflow and recovery decisions

Preflight validates the proposed configuration and ownership. Confirmation freezes
its inputs and physical target. Acceptance creates a durable task; it is not proof
that Docker applied it or that clients can reach it. Readiness, notification
transport and recovery evidence remain separate throughout this workflow.

| Action | Admission and execution boundary | Completion and permitted next action |
| --- | --- | --- |
| Install, upgrade or redeploy | Reviewed immutable revision, current permissions, idempotency and physical identity; only verified execution capacity may write. | Inspect workload, certificate, Gateway and client-access stages independently. |
| Automatic image upgrade | An adopted managed application, a pinned active baseline and an approved update policy. A detected tag change is an activity event. | Inspect the accepted operation and its readiness; a candidate or delivered webhook is not upgrade success. |
| Bind a domain | Owned environment/service and validated route/domain intent. | Wait for certificate validity and loaded Gateway evidence before checking actual client access. |
| Renew a certificate | Configured ACME account/challenge/provider and the existing certificate's ownership. | A current validity window can resolve certificate risk; pending renewal, missing dates or future validity cannot. Provider configuration is not live DNS propagation evidence. |
| Stop | Existing owned application and a frozen execution target. | An intentional stop does not create a lost-target incident. An offline Compose host retains the stop task on its original Engine. |
| Delete or remove runtime objects | Current ownership, ETag and explicit data-boundary acknowledgements. Volumes additionally require physical identity and complete consumer evidence. | Preserve audit and operation history. Runtime deletion is not database or volume rollback. |
| Restore an application revision | Review current ownership, environment changes and the retained-data boundary before accepting a new intent. | Recheck readiness. Restoring a configuration revision does not restore business data. |
| Acknowledge or assign an issue | Authenticated actor, existing enabled responsible user and issue ETag. | Acknowledgement and responsibility do not resolve the occurrence; only confirmed fresh evidence can do that. |
| Silence | Administrator, reauthentication, ETag, scope, reason and an expiry within seven days. | Collection continues. On expiry, remind only still-active occurrences through their selected receivers. |

## Waiting and uncertain execution

`WAITING` means a verified executor, management permission, resource lease or
required Swarm quorum is unavailable before an accepted write. Preserve the task
and restore that prerequisite. Compose and local volumes remain bound to their
original Engine. A verified Manager in the same immutable Swarm ClusterID may
execute eligible cluster operations. Swarm advertise addresses must remain stable
across Manager restarts.

`UNKNOWN` means Docker may have accepted an effect. Preserve the encrypted input,
all attempts and existing resources. Do not create a replacement task, infer
success from replica counts, automatically roll back or delete uncertain effects.
Successors inspect actual resource metadata: cluster configs/secrets through a
verified Manager, and volumes through complete inventory of the frozen Engine.
Inspection has its own timestamp and never authorizes replay by itself.

For Application operations, follow the protected offline
[runtime reconciliation review](recovery.md#review-and-release-selected-work):
stop Controllers, verify current ownership/identity and obtain a fresh receipt
before releasing reviewed work. Standalone configuration, secret and volume
effects require a separate operator decision; the console retains their task and
inspection evidence and does not offer an automatic replay/release action. The
Application reconciliation command must not be used to clear those effects.

## Evidence and the console

The environment overview distinguishes configured prerequisites, current
certificate evidence, historical delivery success and unknown state. Independent
certificate/ACME, notification and HA prerequisites remain visible when image
baselines or workload management are blocked. Environment summaries expose no
receiver destination, signing secret or provider credential.

The Issues and activity page filters stored resource ownership and links an issue
occurrence to operations and receiver deliveries. Transport failures hide old
confirmations; scope changes cancel old reads; authentication loss hides cached
records. Refreshes do not replace unsaved forms or filters. A successful mutation
invalidates related server state, and operation completion refreshes readiness,
HA and environment prerequisite queries.

See [alert routing](notification-delivery.md), [execution](operation-execution.md), and
[layered HA](ha-blueprint.md) for their distinct authority and evidence boundaries.
Database replication/promotion, physical failure domains, backup scheduling and
the S3 recovery center remain separate capabilities, not inferred acceptance.
