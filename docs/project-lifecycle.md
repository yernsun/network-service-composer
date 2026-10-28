# Project lifecycle operations

This change stays on `dev-0.2.0` and does not change the product version. The original archive, reference templates, navigation and environment-switching rules remain the UI authority. The approved project-card settings button is the only added workspace entry.

## Creation and settings

`POST /api/v1/projects` accepts `description`, `default_runtime_id` and an optional `initial_environment`. When an initial environment is supplied, the project, environment, uniqueness claims and idempotency record commit in one transaction. Retry the unchanged request with the same `Idempotency-Key`. Omit `initial_environment` to retain the old project-only behavior.

Runtime selection follows explicit ID, `management.default_runtime_id` (environment variable `NSC_MANAGEMENT__DEFAULT_RUNTIME_ID`), then the sole enabled manageable runtime. Swarm retains its existing PostgreSQL/MySQL requirement. A missing or ambiguous choice is a field error. The project settings dialog can set a default on an existing project, including one without environments. Existing environments never migrate when that default changes.

The original project creation modal includes a default-runtime selector. It shows
enabled, manageable runtime names and submits their IDs. Leave it on Automatic
selection to use the server rule, or choose a specific runtime when several are
available. A `/default_runtime_id` error keeps the modal inputs and focuses the
selector so the same form can be corrected and resubmitted. The selected default
is persisted with the project and inherited by its initial production environment;
this does not set a global default or move any existing environment.

Legacy projects without environments remain reachable through the original Enter Project button, project selector and project-level deep links. Their overview, environment list and project variables do not require an environment. Resource navigation uses that project's empty aggregate scope; it never borrows another project's environment or creates a placeholder. Explicit foreign or missing environment IDs still fail ownership validation. Return to All Projects and use the card's Settings button to review or delete an empty project; entering an environment is not a prerequisite. Unsaved-edit confirmation also applies when switching to an empty project.

Project settings use `PATCH /projects/{id}` with `If-Match: "rev-N"`, and all three fields: `name`, `description`, `default_runtime_id`. The slug and resource ID are immutable. Environment settings similarly accept `display_name`, `kind` and `gateway_ids`. The existing `name` stays the technical slug; display changes do not rename Compose projects or applications. Gateway IDs must belong to the environment runtime's pool, and settings cannot remove gateways still referenced by active ingress.

Empty environment deletion uses `DELETE /environments/{id}` and its ETag. The environment must have no application or hidden application claim; all owned services, ingress and deployment operations are rooted in that claim. The parent project must retain another environment. Deletion and parent-state checks share a transaction with environment creation and deployment binding.

## Project deletion

1. Request `POST /projects/{id}/deletion-previews` with `{ "force": false }`, or explicitly select force in the existing settings dialog. Review names, environments, runtime object identities, blocked references, Gateway Pool scope and retained data. Previews expire after five minutes.
2. Submit `DELETE /projects/{id}` with the project ETag and an idempotency key. The body contains `preview_id`, `force` and `confirm_slug`. The exact slug must match. The server rechecks runtime evidence, membership, unfinished operations and current revisions before accepting.
3. A 202 response includes `Location: /api/v1/project-deletions/{id}`. Query that address; reopen project settings after refresh to restore progress. The project remains visible while the task is pending, blocked or unknown.
4. For a failed, interrupted or unknown result, create and confirm a fresh preview, then `POST /project-deletions/{id}/resume` with the task ETag and the same body shape. The mode cannot change after acceptance. Completed targets and previous attempts are retained. New workload identities need a new review; previously completed applications cannot silently acquire replacements.

Ordinary deletion never stops a running workload to satisfy the precondition. Container `Running`, `Restarting` or `Paused`, Swarm desired replicas and live tasks all block it. Database status, zero ready instances and offline monitoring are not proof of termination. Force first stops/removes explicitly owned workloads; it does not bypass ownership, permissions or observation failures.

The executor uses exact application/service relationships and runtime labels, records actual object IDs, and rejects unreviewed replacements or changed Swarm service versions. Compose removal does not set force or volume-removal flags. Swarm completion requires service absence, observed task termination and actual container disappearance on the inspected node. A terminal task record cannot conceal a still-running or lingering container. A lost Controller lease cancels pending calls; an abandoned running task becomes unknown, and destructive commands require review before resuming.

Project guards prevent deployment, start, update and binding writes throughout deletion. Runtime and project row locks serialize discovery with guard installation and final tombstoning. An ingress guard prevents competing NGINX publication while project ingress is being removed. Other projects' records, configuration and user drafts are retained. Shared domain references, shared HTTP snippets or drafts referencing the affected resources block acceptance. Cleanup waits for fresh loading evidence from every gateway in the reviewed pool scope; accepting a command is insufficient.

Only a fully confirmed task archives the project/environment/control records, hides resource rows and releases the project slug. Task and revision history remain readable. Tombstones reject late mutations and prevent late discovery from reviving the old resource identities.

## Retained data and rollback

Both modes retain data volumes, bind-mounted files, images, networks, secrets/config objects, certificates and shared runtimes/Gateway Pools. There is no standalone “clear services but retain project” operation and no global Docker prune. Retained volume names and mount sources are in the runtime preview; they can require storage administration after deletion.

Compose retains named and anonymous volumes by removing containers without the volume-removal flag. **Swarm deletion is blocked when volume retention cannot be established.** Swarm's task executor uses volume-removing container cleanup, so avoiding an explicit volume-delete API is insufficient. Every current and retained task specification must use an inspectable immutable image reference and account for every image-declared `VOLUME` path. Anonymous mounts, mutable image references, unavailable image metadata and unverified older task specifications produce `project.deletion.data_retention` in both modes.

Provisioned remote Workers are supported through their node-local Controller collectors. Reports are authenticated over mutual TLS, checked against the provisioned Controller instance, Engine and Swarm Node identities and database-session generation, and encrypted in the shared database. A manager-local image with the same name cannot replace Worker evidence. The remote stateful path requires a single replica pinned with `node.id` to its original data node, `stop-first` update/rollback policies, explicit named local volumes, matching Stack ownership and actual task-container mounts. Bind mounts and custom volume drivers are not admitted by this remote stateful path. Manager-local inspection retains its existing named-volume and bind-mount boundary.

Both inventory and storage scans must be complete and no older than 30 seconds. Execution-time storage verification waits for a complete initial report and at least two subsequent sequence advances, bounded to 20 seconds per node; unchanged or unavailable evidence cannot authorize a write. Removal separately waits for complete task/container exit evidence and verifies that retained volumes still have their reviewed node, name, creation time and ownership. A same-name replacement volume is different data. Missing, unprovisioned, offline, stale or incomplete node evidence keeps deletion blocked or its interrupted result visible; force does not bypass these checks. Preserve/migrate unsupported data and inspect a fresh preview before retrying. This implementation never silently converts or recreates retained volumes.

See [remote Worker storage](remote-worker-storage.md) for provisioning, supported cases and recovery steps. Retention is not a backup: losing the original node or data volume requires operator-managed business-data restoration.

The database schema is unchanged: additive fields and lifecycle records use the existing versioned `control_documents` table. Older environment records normalize their missing display name/type on read; runtime inheritance for legacy projects is derived only when existing environments agree. No bulk rewrite or runtime migration occurs at startup. SQLite, PostgreSQL and MySQL migration/conformance tests still apply.

The SQLite driver is pinned to `modernc.org/sqlite v1.54.0`. Real workspace acceptance exposed an older-driver failure in which a failed commit could leave a pooled connection inside a transaction and break later writes. An isolated two-connection regression reproduces the defect and checks that the failed write is rolled back before subsequent writes proceed. The selected release contains the [upstream transaction cleanup fix](https://gitlab.com/cznic/sqlite/-/commit/806ed2d1). This dependency fix does not change the NSC product version or database schema.

Before online replacement, back up the database with the database engine's consistent backup mechanism and retain the current Controller image by immutable digest/tag. Keep gateways, business workloads and data volumes unchanged during rollout. Record the backup, image digest and verification report together.

An older Controller does not understand deletion guards/tombstones. **Do not run the older binary against a database containing in-progress or completed deletions.** A rollback needs a compatible Controller, or the pre-rollout database snapshot with an explicit reconciliation plan. Database rollback does not restore removed containers/services or undo retained-data changes. Recreate workloads only after an operator reviews the retained immutable Compose revision and data-volume mappings; never claim database recovery is business-data recovery. Metadata-only compatibility is additive, but the deletion safety boundary makes arbitrary binary downgrade unsafe.

## Verification

- `make test-coverage-backend`, `make harness`, `make frontend-check`, and applicable race/store checks.
- `NSC_BROWSER_BINARY=/path/to/chrome python3 scripts/e2e-workspace.py`: disposable Docker daemon, actual Controller/gateway/Compose, browser flows, normal/force deletion, volume sentinel and post-discovery checks.
- `NSC_LIFECYCLE_SWARM=true NSC_BROWSER_BINARY=/path/to/chrome python3 scripts/e2e-workspace.py`: separate disposable Swarm daemon and PostgreSQL; lifecycle browser/API and retained-volume checks.
- `npm run e2e:demo --prefix frontend`: original demo flows plus isolated project settings/deletion, with zero business API requests.

Reports, screenshots and traces are saved under `coverage/workspace-e2e/` and the iteration's `coverage/project-lifecycle/` directory. Protocol fixtures and controlled failure tests are distinct from real Docker/NGINX execution evidence. A skipped, failed or environment-blocked check is not a pass. Historical frontend measurements are not current acceptance; read the current thresholds from [the coverage policy](../.agents/rules/coverage.json). Backend admission retains overall 80% branches and incremental 90% statements/branches.
