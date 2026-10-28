# Operations

## HA

PostgreSQL and MySQL support multiple equivalently configured Controllers. Database ownership locks protect each Runtime's event watch, snapshot reconcile and state progression; ACME and GC use separate singleton locks. Application tasks use a durable queue, heartbeats and `deployment:<runtime-id>:<project-name>` leases, allowing one active task per Application. Another Controller can retry after `management.stale_after`. Observation epochs reject stale Docker snapshots. Gateway streams poll Pool revisions every second; PostgreSQL adds LISTEN/NOTIFY. The pinned PostgreSQL 18 image mounts `/var/lib/postgresql` to retain its major-version directory and upgrade layout.

HA Controllers share the database, administrator token, session key, complete encryption keyring, Agent CA trust and equivalent Docker credential references. HTTP/gRPC may sit behind L4/L7 load balancers; gRPC requires long-lived connections.

SQLite permits one Controller only; process locking and configuration checks fail fast. Swarm is prohibited.

## Backup

Back up the database, current configuration, full keyring, configuration-backed DNS credential files, administrator-provided Docker/registry secrets and offline CA. Source Compose, normalized env, resolved Compose, revisions, task inputs and administrator-created DNS profiles are encrypted in the database. Recovery without the matching keyring fails closed and never deploys an empty environment. Retain historical keys for retained backups; live key rotation does not rewrite those backups. Keep credentials and key material separately protected from the database archive.

PostgreSQL：

```sh
pg_dump --format=custom --file=nsc.dump "$NSC_STORAGE_DSN"
```

MySQL：

```sh
mysqldump --single-transaction --routines --triggers nsc > nsc.sql
```

For SQLite, stop the Controller before copying the database, or use the online backup API/CLI. Copying only the main .db while omitting WAL is unsafe.

## Recovery

1. Stop all Controllers while keeping Gateways running on LKG.
2. Restore the database and the exact matching keyring.
3. Run `nsc-controller migrate --config ...` first.
4. Start one Controller; verify `/readyz`, migration version and snapshot revision.
5. Start remaining Controllers and inspect Gateway `applied_config_revision` / `desired_config_revision` and the renderer/model digest tuple.

Database rollback must not overwrite an Agent's LKG with an older revision. If necessary, restore a consistent backup with a higher Pool revision or create a valid configuration change.

Before an upgrade, rehearse the candidate with the isolated PostgreSQL, MySQL and SQLite recovery fixtures in `scripts/recovery-drill.py`. Review its private report, actual backup/restore results and negative checks. A restored Controller must use the persistent preparation barrier and protected recovery mode; normal startup or a running container alone is not recovery acceptance. See [protected recovery](recovery.md) for the backup inventory, complete envelope checks, HTTP-only cases and explicit reviewed release. Keep backup material, matching keys and evidence private.

A Gateway generation volume belongs to its Pool identity. Never reuse it for another Pool. The Agent rejects a nonzero-revision LKG with missing or mismatched Pool identity before starting NGINX.

Inside the Gateway, `/etc/nginx/nginx.conf` is a fixed main configuration including relative `upstreams/*.conf`, `domains/*.conf` and `system/*.conf`. The current immutable generation is `/var/lib/nsc/generations/current/`:

- `upstreams/nsc_{qualified_service}_{suffix}.conf`：Docker Service DNS upstream。
- `domains/{punycode-host}.conf`: one host's port 80/443 entrypoints and all paths.
- `certificates/{certificate-id}.crt|key`: certificates delivered over mTLS; private-key directories use 0700 and files use 0600.
- `system/default.conf`: the default rejecting entrypoint.

Do not edit these files directly. `GET /api/v1/gateway-pools/{id}/config-preview?host=...` exposes redacted previews/digests; `route-trace?host=...&path=...` identifies the Domain, Route, Service DNS, upstream and applied tuple. Publication runs `nginx -t` against the candidate, switches current atomically and sends HUP; failure to load the target revision switches back to LKG.

## Upgrade

1. Read release notes and verify image digests and SBOMs.
2. Back up the database and keyring.
3. Rehearse migration against the backup in a maintenance environment.
4. `nsc.gateway.v2` requires matching Controller/Gateway versions; upgrade both in a maintenance window, Controller first.
5. Stop the previous SQLite Controller before upgrading; do not overlap instances.
6. Verify applied/config/probe revision metrics (`nsc_gateway_applied_config_revision`, `nsc_gateway_config_revision_lag`, `nsc_gateway_probe_revision_lag`), NACKs, ACME retries and Service DNS readiness.

Migrations run forward only. Rehearse recovery from a complete backup before release.

## Troubleshooting

- Runtime OFFLINE: retain configuration and Service identity, mark observations stale; instance address changes do not rewrite NGINX upstreams.
- Operation stuck RUNNING: inspect worker heartbeat, deployment lease and Docker endpoint. Never manually mark it successful. Expired leases permit idempotent recovery.
- Candidate deployment failure: with an active revision, expect ROLLED_BACK or ROLLBACK_FAILED. A failed first install removes only objects created by that attempt, retaining named volumes and failure history.
- Stack execution failure: ensure the endpoint is a manager. Swarm stop uses stack rm and waits for disappearance; start rebuilds the saved revision with replica overrides.
- No Agent: do not infer Service DNS probe success; existing LKG continues serving.
- NGINX NACK: inspect Gateway last_error; the rejected candidate is discarded and the previous generation remains active.
- Missing DNS/overlay/alias: inspect route-trace and config-preview. Resolution failures retain NGINX 502, connection/response timeouts retain 504. Only an explicitly paused Route returns NSC's 503 with route_paused.
- Application 503: find the Gateway JSON access log using X-Request-ID. upstream_status=503 originates from the application; failure_reason=route_paused identifies local NSC rejection.
- ACME HTTP-01 pending: verify an online Gateway has ACKed the distribution_revision containing the challenge location, then check public port 80.
- ACME DNS-01 pending: inspect the redacted dns_provider_ref and profile source. For config profiles, check the readable credential file and equivalent configuration on every Controller; for database profiles, check the available keyring and current profile revision. Then check zone/TXT permissions and authoritative _acme-challenge TXT propagation. DNS-01 does not wait for a Gateway ACK. Do not print credentials to troubleshoot a challenge.

## Metrics and logs

The internal listener exposes runtime, discovery, instance, Service-DNS probe, Gateway lag, certificate and ACME metrics. Key snapshot/NGINX metrics include:

- `nsc_snapshot_generation_duration_seconds`、`nsc_snapshot_compressed_bytes`
- `nsc_nginx_validation_total`、`nsc_nginx_validation_duration_seconds`
- `nsc_nginx_reload_total`、`nsc_nginx_reload_duration_seconds`
- `nsc_deployment_operations_total`、`nsc_deployment_queue`、`nsc_deployment_duration_seconds`
- `nsc_catalog_sources`

Use low-cardinality runtime type, pool, status, operation and result labels; do not label Prometheus series with Service/Endpoint IDs. Compare histogram_quantile(0.95, ...) for snapshot generation, validation and reload latency with desired/applied revisions to assess propagation time.

JSON slog output includes request/resource IDs and states, never tokens, private keys, complete bodies or plaintext environments. Operations retain bounded redacted logs; audit events contain actor, action, resource and request SHA256, not request content. Alert on gateway rejected snapshot, certificate job failed, runtime worker stopped, ROLLBACK_FAILED and deployment queue growth.
