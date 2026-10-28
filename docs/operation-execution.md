# Durable node execution and failover

Controller leadership schedules work; it does not establish that the elected
process owns the Docker Engine. Node-bound work uses a separate authenticated
operation endpoint on the selected Controller. SQLite retains its single-owner
execution path and cannot admit the distributed executor.

## Admission and authority

Queue admission captures the Runtime's physical configuration and provisioned
Engine/node identity inside the same SQL transaction as the operation. New node-bound tasks require a complete enrollment and a known Swarm ClusterID; admission rejects partial physical snapshots atomically. Later
display-name or observation updates do not change this snapshot. An established
ClusterID is immutable; changing an endpoint or credential binding is rejected
while owned Applications or pending operations remain.

The internal mutual-TLS request contains only the operation ID, attempt, Leader
term and selected target identity. It cannot carry a command, Compose source,
secret content, arbitrary URL or replacement inputs. The executor loads the
persisted attempt and frozen encrypted input. A domain-separated input MAC uses
the shared session key, so stored task metadata does not expose a plain hash of
low-entropy secret content. Storage encryption rotation preserves this identity;
changing the shared session key fences existing attempts.

Every attempt records its selected instance, node, Engine, observation generation
and actual acceptance. The Leader retains Application/storage leases and task
heartbeats. The executor holds an additional attempt-specific SQL session lease.
Current term, attempt, lease owners, management settings, frozen Runtime and
resource ownership are checked before work, before commands and during execution.
Metadata activation and task completion use transaction-local authority checks;
they do not open another connection while holding a task transaction.

The internal resource endpoints retain read-only inventory support. Config,
secret and volume mutations must enter through the frozen operation endpoint.
Internal clients use provisioned HTTPS/mTLS identities and disable proxies,
redirects and automatic acceptance-request replay.

## Selecting an executor

Compose uses the original provisioned Engine. An offline host produces retained
`WAITING` evidence, with no business failure or host migration. Enabling management
or restoring the original host lets a later claim continue the original intent. A Controller without the node execution capability also retains existing node-bound work instead of using the legacy command path.

Swarm prefers the configured Manager. An alternate must have the same nonempty
immutable ClusterID, exact provisioned Engine/node identity, a current collector
lease, fresh complete Manager/quorum evidence and the operation-execution
capability. Older Controllers without that capability are not eligible. The
configured Runtime location remains unchanged; the console separately displays
the actual executor. Application data-node constraints are retained.

Volumes remain pinned to the Engine/node in the frozen volume request, including
when the Runtime is a Swarm cluster. Manager failover is not volume migration.
Unavailable identity, quorum or consumer evidence cannot authorize removal.

## Acceptance, uncertainty and recovery

Acceptance returns promptly while execution continues under the Controller's
lifetime and durable fences. The operation row decides completion; an HTTP
response is not proof that Docker applied the intended workload. Duplicate
acceptance observes the same attempt instead of executing it again.

A mutation marker is persisted before a Docker write. Transport loss, lost
session ownership or an unconfirmed completion retains `UNKNOWN` and the frozen
input. Cancellation or executor loss before the write marker retains `WAITING`; a late cancellation callback cannot replace it with a fabricated mutation result. The Leader releases scheduling resources instead of waiting indefinitely
for a dead executor. No rollback, cleanup or repeated mutation is authorized by
a missing response. A successor first obtains fresh complete inventory of the
same physical target and records that inspection. Matching replica counts alone
cannot prove which restart or rollout was accepted.

Accepted uncertainty survives repeated claims, unavailable observers and missing
intermediate attempt records. A later `WAITING` record cannot erase an older
write marker. Only an explicitly reviewed reconciliation closes that history;
restoring a node never authorizes an automatic replay of an unresolved write.

For unresolved effects, preserve the task and follow the protected
[runtime reconciliation and release procedure](recovery.md#review-and-release-selected-work).
This is an explicit offline maintenance action: stop Controllers, retain their
history and inputs, inspect owned resource identities/versions, and obtain a
fresh authenticated receipt. Reconciliation marks the reviewed attempt before
the next claim can execute again. A legacy task without an admission snapshot
requires this explicit identity review; startup never silently binds it to a
possibly edited Runtime. Swarm inspection may use a provisioned local Manager
in the same ClusterID; Compose still requires its original Engine.

This workflow does not restore application databases or volumes, prove client
availability, implement database promotion, or provide an ingress failover
service. See the [layered HA boundary](ha-blueprint.md).

## Verification evidence

Native boundary tests cover immutable admission, reference-only mTLS requests,
late attempts/terms, input authentication, original-Engine waiting/resume,
uncertain acceptance and retained execution evidence. Shared PostgreSQL/MySQL
fixtures exercise actual session locks and late transaction rejection separately
from simulated Docker results. Optional fixtures use
`NSC_TEST_EXECUTION_POSTGRES_DSN` and `NSC_TEST_EXECUTION_MYSQL_DSN`; provision only
isolated stores and keep credentials outside Git and command arguments.

Real Controller/Manager crashes and runtime failover must be exercised with the
current build in isolated Docker fixtures. Record source/image identity, failure
time, actual executor, preserved workload/data identities and cleanup. Unit or
SQL fixture success alone does not establish production or end-to-end HA.
