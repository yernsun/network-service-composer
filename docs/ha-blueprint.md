# Layered high availability acceptance

This blueprint is for trusted administrators operating Compose and Swarm.
Controller replicas protect control-plane ownership; they do not replicate the
database, provide an ingress VIP, or recover application data. SQLite is a
single-Controller, Compose-only deployment and is outside the HA topology.

## Topology and contracts

Use at least three Swarm managers in independent failure domains, three
equivalently configured Controllers, a single writable PostgreSQL/MySQL endpoint
with a fenced standby, two independently stored Gateways, and two ingress paths.
Keep DNS, registry access, TLS trust, external monitoring and backup storage
independent of the hosts they protect. A same-host Compose topology is a fixture,
not a failure-domain deployment.

| Layer | Health and failover contract | Recovery owner |
| --- | --- | --- |
| Controller API | Check `/readyz`, not process existence. Drain unhealthy API peers. Reconnect long-lived gRPC streams; keep Agent and internal-controller mTLS identities distinct. | Platform operator |
| Database | All Controllers use the same single-writer endpoint. Preserve session locks with session-affine/direct TCP connections. Fence the old writer before promotion; prove the target replayed the chosen recovery watermark. | Database operator |
| Gateway | Check the business listener using the expected Host/SNI and route. Controller disconnection alone does not make a valid LKG unhealthy. Each Gateway owns its generation volume and identity. | Network operator |
| Ingress | Two externally reachable LB paths or a managed HA LB, independently checked. TLS passthrough must preserve SNI; forwarded addresses are trusted only from configured proxies. | Network operator |
| Swarm | Three managers tolerate one manager loss. Never force a new cluster merely to make a UI green. Preserve immutable node/engine/cluster identity. | Cluster operator |
| Business data | Explicit data owner, database-consistent backup, off-host destination and a tested restore. A named volume on one host is still a single point of failure. | Application/data owner |

Run Controller migrations once before admitting peers. Share the complete
keyring, session key, trust roots, credential references and DNS profile sources.
Pin Controller/Gateway versions and OCI digests. Do not mix unmatched protocol or
schema versions. Keep the offline CA and backup key material outside the online
database backup.

The existing Compose configuration supplies service defaults. The additional
[HA placement example](../deploy/ha.compose.yaml) requires explicit per-instance
configurations and secret directories; it does not provision DB replication,
ingress failover, host storage or production networking. Place replicas on
separate hosts through the infrastructure owner, with the same externally
managed single-writer DSN. Do not expose internal or metrics listeners publicly.

## Failure handling and acceptance budgets

The isolated acceptance budgets below are test targets, not production SLOs.
Measure from injected failure to verified progression/traffic. Record loss and
time explicitly; a passing readiness response alone is insufficient.

| Scenario | Required evidence | Fixture budget |
| --- | --- | --- |
| Controller crash or DB-only partition | One current owner, monotonically advancing observations, former owner excluded after reconnect | 120 seconds |
| Planned database switch | Writers quiesced, replay watermark checked, old writer fenced, all Controllers reconnect and see committed records | 120 seconds after promotion |
| Healthy Gateway update | Expected revision/model/rendered/disk tuple on each target; continuous HTTP/TLS requests without errors | Zero sampled errors |
| One Gateway or LB unavailable | Surviving path serves the expected route and certificate; failed path remains visible | 30 seconds |
| All Controllers unavailable | Gateways continue serving LKG; no claim that renewal, discovery or new publication remains available | Continuous sampled traffic |
| Data node or quorum unavailable | Destructive operations blocked; immutable resources and operation history preserved | No destructive side effects |

Existing connections cannot migrate between failed processes. Test new requests
and surviving SSE/WebSocket streams separately. HTTP/3 requires a separate
UDP-capable ingress test and is not established by HTTP/TLS checks.

An asynchronous replica may lose committed data on an unplanned promotion.
Reject promotion when the selected recovery watermark is not present. Production
RPO zero requires a separately managed synchronous replication/quorum design.
NSC is not a database failover operator and does not protect two divergent
writable database primaries.

## Alerts and exercises

Load [Prometheus rules](../deploy/ha-alerts.yaml) alongside existing metrics.
Route alerts through an independently operated Alertmanager. Supply receiver
credentials outside Git and check both firing and resolved notifications using
an owned test receiver before relying on the route. Acknowledging an NSC issue
does not repair the underlying condition or silence external health monitoring.

Add external probes for each ingress path, registry/DNS availability, backup age,
Swarm manager quorum, storage exhaustion and database replication/fencing.
Controller metrics cannot prove those layers healthy. The included rules use
existing metric names and deliberately do not synthesize absent metrics as zero.

Run `scripts/e2e-ha.sh postgres|mysql` for owner crash/restart and health events,
then `scripts/e2e-ha-failover.py postgres|mysql --image <current-controller-image>
--output coverage/p0/r04/<driver>.json` for real replication and network partition.
Use the real Gateway acceptance command documented in `gateway-acceptance.md`.
Run `python3 scripts/test-swarm-quorum.py --output coverage/p0/r05/quorum.json`
to exercise three disposable managers. It proves one manager loss preserves
writable quorum; after two losses, failed or timed-out inventory remains UNKNOWN
and no destructive commands are attempted. Quorum recovery must preserve the
fixture Service identity and its committed state.
Retain private, source-bound evidence and cleanup only the fixture's resources.
Never run these exercises against existing business containers or a host Swarm.

Before admitting a stateful application, rehearse restoring its data on a new
node, verify application/schema compatibility and record the restore duration.
Rehearse both complete control-plane outage and loss of an ingress failure domain.

## Live layer evidence

Administrators can open `/infrastructure/ha` or read `/api/v1/system/ha`.
The endpoint is read-only, uncached and rejects probe parameters. It returns
separate Controller, execution, Swarm quorum, Gateway, external-ingress and
storage layers with observation times, sources and bounded reason codes. It
never returns internal URLs, session owners, credentials or raw command output.

Leader readiness requires a live term and running scheduling workers. Follower
readiness additionally checks an authenticated, term-bound round trip to the
Leader's private readiness endpoint; a database lease alone is insufficient.
Recovery holds and expired authority prevent normal readiness. Signed node
inventory needs a live collector session to establish execution or quorum.
Quorum loss blocks writes without deleting workloads or their history.

Gateway configuration/startup confirmation is separate from externally observed
business traffic. Each Gateway retains its own identity and durable generation
state. A valid last-known-good generation may serve during a control-plane
outage; an empty generation directory is not equivalent to a tested cold start.
The overview does not infer external ingress availability from a heartbeat.

Storage connectivity is reported as limited evidence. This change neither
provisions database replicas nor verifies their failover. External ingress is
unknown until independently monitored, so `end_to_end_verified` remains false.
No combination of locally healthy cards upgrades these unverified dependencies
into an end-to-end HA claim.

## Monitoring outside NSC

Deploy [Prometheus](../deploy/prometheus.external.yaml) and
[Alertmanager](../deploy/alertmanager.external.yaml) outside the NSC failure
domain. Replace the example Controller targets and both ingress targets. Run a
blackbox exporter with an `http_2xx` module and a business route that exercises the
expected Host/SNI on each entrypoint. Keep the monitor and alert receiver alive
when all NSC Controllers stop; do not point this receiver through NSC itself.
Alertmanager payloads use their own protocol, independent of NSC's signed Webhook.

Operational metric collection exposes attempt, last-success and failure gauges.
A failed read retains previous numerical values but never advances success time.
The independent rules detect failed/stale collection on an active scheduler,
missing Controller targets, absence of scheduling and individual ingress loss.
A process scrape succeeding is not proof of business traffic or data availability.

### Current-build node execution HA fixture

Run `python3 scripts/test-node-execution-ha.py --controller-image <current-local-controller-image> --output coverage/node-execution-ha/summary.json` after building the Controller image with the pinned Docker CLI and Compose plugin. The fixture uses three owned nested Engines, independent Controller identities and a shared temporary PostgreSQL database. It never mounts the host Engine socket into a Controller or contacts an existing workload. Workload images must already be cached.

Acceptance includes authenticated Leader/Follower readiness, Controller crash takeover within 120 seconds, a paused executor returning after its SQL sessions and term have been replaced, same-ClusterID Manager selection, original-Engine Compose waiting, and loss/recovery of Swarm quorum. It records every listener sample, preserves unresolved operation history and compares workload/resource identities. Cleanup verifies ownership labels. The report records image and binary identities.

This is a single-host isolated failure simulation, with no database replication or promotion. Use `scripts/gateway_acceptance.py --verify-lb-redundancy` separately for independent Gateway identities, persisted LKG cold start, control-plane interruption, reconnect/configuration convergence and two ingress processes. Real physical failure domains and external storage/ingress dependencies require separate deployment evidence.
