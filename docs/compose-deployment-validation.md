# Compose deployment validation

The four-step application wizard validates YAML with `yaml` and the unmodified,
pinned Compose Specification schema. The backend uses the same schema and fixtures
in `internal/composecheck/testdata/conformance.json`. Schema updates are explicit;
the browser and Controller do not download schemas during validation.

`POST /environments/{id}/compose-validation` is a read-only analysis endpoint.
It accepts `compose_yaml`, `network_name`, and `deployment_plan`, resolves the
selected environment, and returns diagnostics, mount summaries, and current Swarm
nodes. YAML and schema failures stop semantic analysis. Values such as
`replicas: abc` are rejected by execution semantics because the official schema
permits interpolated strings. Diagnostic values do not include environment secrets.

## Storage and placement

Standalone deployments do not require placement for local storage. In Swarm,
local named or anonymous volumes and bind mounts require at least one valid
placement constraint. Preferences alone do not restrict nodes. External volumes
and custom drivers need an explicit local/shared classification. `tmpfs` is not
persistent storage. Driver options and sensitive host paths remain restricted.

The service workspace edits storage declarations per mount and placement per
service. Declaring a mount shared requires explicit confirmation that all eligible
nodes can access it. This declaration does not provision, move, or replicate data.
Mixed storage still requires placement whenever any mount is local. Bind source
paths must exist on eligible nodes; matching nodes does not check path existence.

Only Ready/Active nodes matching every constraint count. Invalid constraints,
conflicting constraints, unavailable evidence, and zero matches block deployment.
Multiple eligible nodes and multiple local-storage replicas require warning
confirmation. An unknown storage capability cannot be dismissed as a warning.

The workspace changes the YAML document AST, preserving unrelated configuration
and comments. Conflicting constraints require explicit replacement. Anchor
consumers receive local edits without modifying other services. Declaration
fingerprints bind resource definitions and consumers; changed mounts must be
confirmed again. Declarations are stored in the deployment plan and immutable
revision configuration source, never injected into user YAML.

## Admission and review

Input changes invalidate analysis and review immediately. Typing is debounced
400 ms. Imported files, examples, constraint application, and submission trigger
fresh checks. Out-of-order responses cannot overwrite current evidence.

An encrypted review freezes the selected environment, network, YAML and plan.
The Controller rechecks admission when creating deployment operations. The Worker
rechecks live node evidence before changing Docker. Installation, revision,
start/redeploy, restore and automatic updates use this admission path. Restart
and scale also recheck the active revision; increasing local-storage replicas
requires a newly reviewed revision if the scale warning is not acknowledged.
Stateful Swarm scale operations require exactly one replica. Updates preserve the
reviewed data node, named volumes and mount targets. No data is automatically
copied and existing applications are not migrated.

## Stateful Swarm stop and start

A stateful STOP requires the active revision's fixed data node and complete,
fresh per-node evidence of actual task containers, image-declared volumes and
explicit named mounts and volume project ownership labels. The Controller records the node, volume names and creation
identities before removal. It confirms task containers have exited and the volumes
remain after removal. Application deletion preserves those volumes.

The first installation rejects pre-existing, unrecorded data volumes.
Before restarting or updating, the Worker waits for new collector reports and
verifies the original volumes on the original node. Offline nodes, expired or
incomplete reports, externally changed mounts, missing volumes and unknown results
fail closed. The first storage mutation and an uncertain STOP leave a durable recovery marker; START stays blocked
until a retry of STOP verifies task exit and retained volumes. A failed initial installation without a retention record cannot be
started automatically. Legacy Swarm applications without reviewed storage placement
remain subject to the retention guard. This release provides no data migration.
