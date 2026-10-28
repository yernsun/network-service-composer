# Protected recovery and isolated drills

A Controller database restore does not restore application databases, volumes, or host files. A configuration rollback does not undo a business-data migration. Recover those materials with the application's own consistent backup method and a compatible application/data schema version, and record which team owns each archive and restore action.

## Backup inventory

Copy the [inventory template](../examples/recovery/inventory.template.json) into a new private directory with directory mode `0700` and file mode `0600`. Replace every placeholder using observed identities and checksums. Keep it outside Git, ordinary diagnostics, and public build artifacts. Do not put credentials in the inventory; reference separately protected configuration, the full matching keyring, file-backed DNS credentials, Docker/registry/peer credentials, and offline CA material.

Use a consistent PostgreSQL logical dump or a MySQL consistent dump with the required transactional and locking guarantees for the tables in use. For SQLite, use the SQLite backup API or stop every writer before copying the database; copying the main file while WAL writes continue is not a backup. Verify archive checksums before restoring. Record the NSC source/image/schema, application image and data-schema compatibility, immutable engine/node/cluster/storage identities, required RTO/RPO, backup owner, and the last actual restore result. An application directory or a volume name alone cannot prove the original storage identity.

Retain historical encryption keys for retained database backups. Live `keys reencrypt` does not rewrite old archives. An unavailable file-backed provider or wrong keyring is a failed recovery prerequisite, not permission to substitute empty values. See [Security](security.md#secret-encryption-and-dns-credentials).

An `ACTIVE` or `RENEWING` certificate, or any certificate with stored PEM material, requires its encrypted private-key envelope. Missing key material fails inspection without changing the restore. An unissued pending/authorizing/validating certificate or an issuance failure with no PEM may honestly have no private key yet; an HTTP-only installation does not need a fabricated certificate.

## Recover into a protected target

1. Stop all Controllers that can access the restore target. Use a private database and an internal-only network for rehearsal; do not attach a Docker socket, publish ports, or give the target a route to business systems. Check archive checksums, then restore the database and separately protected material. Any required forward migration is an explicit offline step, never an automatic recovery startup action.
2. Set `controller.recovery_mode: true` (or `NSC_CONTROLLER__RECOVERY_MODE=true`) and `storage.auto_migrate: false`. Use the restored keyring and existing persisted user identities. Configured bootstrap credentials are not imported in recovery mode.
3. Run `nsc-controller recovery-check --config /private/recovery.yaml`. It first requires the restored migration journal to match the embedded migration authority: the current version and every expected applied migration must match, and no unknown migration may remain applied. An older, newer, incomplete or malformed journal fails without initializing it or migrating; rolled-back unknown entries remain inert historical records. Migrate a compatible private restore explicitly before protected inspection. It decrypts all available certificate/ACME account keys, Application revision fields, durable Operation inputs and encrypted control documents, including variables/history, DNS profiles, restore/release records, source drafts, node reports and hook secrets. The check is read only and works with PostgreSQL, MySQL and SQLite. It does not launch deployments or require an invented certificate in an HTTP-only installation.
4. If real Gateway revision/digest evidence is available, supply `--evidence /private/gateway-evidence.json`. The existing array format contains `gateway_id`, `pool_id`, `applied_revision` and `model_digest`. A missing Pool, an older database, or a same-revision digest mismatch fails. Without evidence, the report states `gateway_evidence: not_supplied`; zero encrypted values reports `key_verification: no_encrypted_material`. Neither is evidence of a real Gateway load or successful access.
5. Run `nsc-controller recovery-prepare --config /private/recovery.yaml --output /private/new-review.json`. The output is created exclusively with mode `0600`. Preparation persists a startup barrier and holds restored nonterminal deployment/deletion tasks, ACME certificates, and managed Domains whose certificate job is missing. A normal restart is rejected while the barrier exists. Repeating preparation into a different new file recovers the original plan if the baseline is unchanged; it does not overwrite an earlier plan or rebaseline changed data silently.
6. Start `serve` with the same recovery configuration for authenticated inspection. `/healthz` remains available; `/readyz` returns `503`, and responses carry `X-NSC-Recovery-Mode: read-only`. Resource and webhook writes return stable `recovery.read_only` problems. Login/logout change cookies only. Startup does not migrate, seed identities/catalogs, elect a leader, run discovery/deployment/certificate/deletion/GC workers, collect node state, or connect a local Gateway. Internal Controller and Gateway listeners are not started. A successful process start is not production readiness.

The prepare barrier must precede `serve`; disabling the configuration flag cannot bypass it. Preparation and release are privileged offline maintenance commands using database access, not remote management APIs. Keep every Controller stopped during those maintenance commands and use protected configuration files. They acquire the existing SQLite process lock or PostgreSQL/MySQL scheduler advisory lock and refuse an active owner. These locks complement the offline requirement; they do not make maintenance on a live multi-Controller database safe. A newly elected scheduler rechecks the persisted barrier before starting its workers.

## Review and release selected work

Inspect the private plan against recovered business data and the current immutable runtime/storage identities. Review fixed operation inputs, target versions, ownership and fresh runtime observations. Restore user access and trust material deliberately; changing a configured password cannot recover a missing persisted identity directory.

The plan defaults `activation_reviewed` to `false` and every item's `decision` to `hold`. After reconciling database, application-data and runtime state, set `activation_reviewed: true` and change only explicitly approved items to `resume`. Keep all item identities, versions, input digests, order and the overall fingerprint unchanged. Run:

```sh
nsc-controller recovery-release --config /private/recovery.yaml --review /private/reviewed.json
```

Stop the inspection process before release. Release validates the current resource/input fingerprint and barrier revision in one transaction, archives the reviewed decisions, clears only the approved holds, and removes the startup barrier. A stale/modified plan, changed database, extra item, unknown decision, or missing hold fails without clearing protection. Explicitly restart with normal configuration afterward; release never starts workers itself. Unapproved holds survive normal restarts and are excluded from queue claiming and stale-operation requeue.

A held `RUNNING` Application operation requires deliberate runtime reconciliation before release. From a maintenance client with reviewed access to the recovery database and intended engine, use the original engine/node/cluster identities in the private backup inventory:

```sh
nsc-controller recovery-reconcile --config /private/recovery.yaml \
  --operation original-operation-id --expected-engine original-engine-id \
  --expected-node original-node-id --expected-cluster original-cluster-id \
  --output /private/new-reconciled-review.json
```

Omit node/cluster arguments only when the original engine has no such identity. The command reads Docker Info and the existing ownership/retention observation path; it does not execute an operation or change its status. It requires persisted managed Application/Service claims, correct engine/node/cluster identity and fresh, unambiguous object/version evidence. Node-bound Compose execution additionally requires its original configured local Engine. Swarm inspection may use another provisioned local Manager in the same immutable ClusterID. Unavailable remote storage evidence cannot authorize recovery. `management.enabled` must permit that existing inspection path while `recovery_mode` continues to block resource writes. Keep automated drill networks isolated; this deliberate maintenance read is not automatic production probing.

The new plan has a `reconcile` decision with an authenticated receipt bound to the operation revision and input digest. Finish the activation review immediately before release: the receipt expires after two minutes, and release recollects actual runtime evidence to reject drift, stale/offline observations or edited receipts. Optional `--review` extends an existing private plan when several Applications need reconciliation. Release removes the reviewed hold and marks a recorded node-execution attempt `RECONCILED`; the original `RUNNING` status, attempt count and encrypted input remain unchanged until the next stale claim. For a legacy task without a physical snapshot, the signed receipt must contain the reviewed Runtime and provisioned original Engine identity; release freezes that identity atomically. Existing nonempty snapshots are never rewritten. The tool does not guess success or blindly execute old commands. A held Application remains blocked until this explicit observation succeeds.

For an interrupted project-deletion item, the reviewed `defer` decision records `UNKNOWN`, archives the original task and retains completed-object history. It removes that task's hold but does not remove resources. After normal startup, use the existing fresh-preview Resume flow, which checks ownership, current versions and data-retention evidence again.

Pending notification events, delivery attempts and deferred silence reminders are
also held. The default `hold` decision keeps them outside due-work queries after
normal startup. An explicit reviewed `resume` permits reprocessing through the
current routing and ownership checks. A `SENDING` receipt may already have been
accepted by its receiver; retry retains the same signed event ID. See
[notification delivery](notification-delivery.md) for migration and dedupe limits.

`AUTHORIZING`, `VALIDATING` and `RENEWING` certificate/provider outcomes remain held. Check the CA order/account and authoritative TXT state, confirm which certificate was issued and which Gateway actually loaded it, and retain evidence of any required provider cleanup. A verified certificate can be imported/rebound through the existing certificate workflow; request a fresh managed certificate only after resolving the old provider-side work. Do not delete a hold or rewrite operation status with ad hoc SQL to obtain a green report.

If restored state changes during review, release fails closed. Retain that failed attempt and inspect the cause; repeat the restore into a fresh isolated target when a consistent baseline cannot be established. Keep the review and verification report with the private backup inventory. Report actual restored business-data checks separately from envelope verification and Gateway evidence.

## Drill acceptance

The repository recovery drill uses isolated fixtures and pinned image references. Run it for PostgreSQL, MySQL and SQLite against the candidate Controller build. It must verify a real backup/restore, preserved queued/interrupted history, a durable preparation barrier, protected HTTP/authentication behavior, unchanged data during inspection, wrong-key rejection, stale Gateway-evidence rejection where evidence exists, and selective release. Retain failures and cleanup results in a new private report directory; do not publish copied credentials or host metadata.

No rehearsal modifies a live installation or proves live provider access. OVH-specific verification remains **disabled**. A report that lacks Gateway evidence or application-data restore evidence must retain those limitations explicitly.

The archive-only drill invocation is:

```sh
python3 scripts/recovery-drill.py --driver postgres \
  --backup /private/database.dump --sha256 verified-archive-sha256 \
  --config /private/recovery.json --image nsc-controller:candidate \
  --output /private/new-drill --material-directory /private/material \
  --auth-token-file /private/restored-token
```

It accepts `postgres`, `mysql` and `sqlite`, optional real `--evidence`, and explicitly supplied backup/configuration inputs. Supply `--auth-password-file /private/restored-password` and, when needed, `--auth-username restored-user` to verify actual password login and signed-cookie access after the user's password has changed; the persisted API token is not assumed to be that password. It never takes an implicit backup from live source containers. Keep matching archive checksums, source fingerprints and image digests with the private report.

For development acceptance, the fixture scripts create their own source databases, actual native archives and internal-only restore targets. Use a new report directory for each run:

```sh
python3 scripts/e2e-recovery.py --image nsc-controller:candidate \
  --output coverage/recovery-e2e/new-run
python3 scripts/e2e-recovery-reconciliation.py --image nsc-controller:candidate \
  --output coverage/recovery-reconciliation/new-run
```

The first command exercises all three stores by default; `--driver` selects one. The second starts an isolated nested Docker daemon and verifies real owned Compose objects, wrong-engine/offline/drift refusals, receipt release and one subsequent stale claim. Each run resolves the candidate image to its immutable local image ID. Synthetic newer-revision evidence is a labeled rejection test, not real Gateway-loading acceptance. These fixtures do not claim an application business-data migration, real certificate issuance or a production Gateway load.
