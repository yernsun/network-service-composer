# Remote Worker storage safety

Swarm application management uses a manager for scheduling and node-local
collectors for storage evidence. Each participating node runs a provisioned NSC
Controller with access to that node's Docker Engine. Controllers share a
PostgreSQL or MySQL database and encryption keyring; SQLite does not support
Swarm management. Follow [Controller deployment](controller-global-deployment.md)
to provision distinct instance certificates and immutable node/engine bindings.
Discovery does not enroll a node or grant ownership of its storage.

## Supported and blocked cases

| Case | Storage admission |
| --- | --- |
| Named local volumes, one replica pinned to its original provisioned Worker | Supported with complete current image, volume and actual container evidence, matching Stack ownership and immutable image references. Raw definitions must include `stop-first` update/rollback policies as well as the fixed node and replica count. |
| Stateless remote tasks | Supported when every relevant node has complete current evidence and the immutable image/task specifications have no implicit data volumes. |
| Remote bind mounts, custom drivers, multiple stateful replicas or changing the data node | Blocked by the remote stateful retention path. Back up or migrate the data through an operator-owned process. |
| Node not provisioned, unavailable collector, incomplete inventory/storage scan or expired observation | Blocked; cached manager metadata and force deletion cannot substitute for evidence. |
| Same volume name with a different creation time or owner | Blocked as a changed data identity. Restart must not create or adopt empty replacement storage. |
| Anonymous or uncovered image-declared `VOLUME` | Blocked before Swarm service removal, including paths in retained older task specifications. |

Reports use mutual TLS, the provisioned Controller instance/Engine/Node binding,
and a database-session generation with monotonically increasing sequences.
Stored reports are encrypted. Ordinary metadata displays may retain an old
observation, but destructive admission requires complete evidence no older than
30 seconds. It rejects observations more than five seconds in the future.
Execution-time storage verification waits at most 20 seconds per node for a
complete report followed by two sequence advances; a collector restart must
provide the new generation and sufficient new reports. Direct manager-local
SDK inspection performs a live scan rather than pretending to be a remote
collector report.

## Retry and recovery

When a node is unavailable, restore its collector and verify the provisioned
identities before retrying. Do not update bindings to a replacement Engine just
to make an existing application's data appear available. Named volumes are
node-local; equal names on different nodes are separate data stores.

After an uncertain deletion, inspect the retained operation history and actual
runtime objects. Create a fresh deletion preview and explicitly resume the
existing task. Previously completed targets remain recorded. A failed or
interrupted stateful installation retains its recovery marker; a new revision
cannot bypass that marker. Loss of the original volume needs a reviewed
business-data restoration, not a configuration rollback.

Project/application deletion retains data and never runs Docker prune. The
separate [volume management API](volume-management.md) intentionally deletes an
individual unused volume after explicit data-loss acknowledgement and additional
live consumer checks. Its permission to discard data does not relax application
restart or project-deletion safety.

## Isolated verification

Build the current Controller binary/image and run:

```sh
NSC_RETENTION_ACCEPTANCE=true python3 -B scripts/e2e-market-swarm.py
```

The fixture creates two disposable nested Docker engines, provisioned mTLS
collectors and a temporary PostgreSQL database. It verifies remote retained
data through STOP/START, missing/stale collector blocking, force-deletion safety,
same-name replacement rejection and retained sentinel data. Reports are private
and stored under ignored `coverage/remote-retention/`. Boundary tests separately
cover incomplete reports, changed generations and unprovisioned nodes. Those
protocol cases do not claim a real network failure or production deployment.

Set `NSC_CONTROLLER_IMAGE` and `NSC_CONTROLLER_BINARY` to artifacts built from
the intended source. The fixture uses locally cached images; it does not use
the installation's Docker daemons, credentials or data. Keep private fixture
configuration, node bindings and diagnostic reports out of Git.
