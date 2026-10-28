# Deployment readiness evidence

The operation page displays five separate stages: durable request acceptance,
current workload convergence, certificate issuance, exact Gateway configuration
loading and actual client access. A `SUCCEEDED` operation remains the recorded
execution result. A later observation failure does not rewrite that history,
and a successful execution does not establish that clients can reach a service.

`GET /api/v1/operations/{id}/readiness` uses the existing authenticated operation
read boundary and returns `Cache-Control: no-store`. It performs reads only:
it does not retry operations, issue certificates, publish Gateway configuration
or connect to a business hostname. Viewer responses contain public object and
configuration evidence, not private keys, deployment inputs, runtime connection
addresses or raw diagnostic logs. Existing entrypoint status and retry APIs
remain available independently of this observation.

Stage states are `PENDING`, `CONFIRMED`, `FAILED`, `UNKNOWN` and
`NOT_APPLICABLE`. Missing or stale observations remain unknown. Known execution
or loading failures remain explicit; partial Gateway results are listed
individually. Certificate issuance requires a usable current asset, while
Gateway loading requires the required Pool revision and matching model,
rendered and on-disk digests from fresh confirmation evidence. Neither stage
proves external DNS, TLS termination or an HTTP request from a client network.

Workload evidence checks the operation's current application revision and
selected service identities, current authoritative Runtime inventory and fresh
service counts. Managed workload confirmation also requires Service membership
in the same accepted authoritative Runtime inventory. An absent managed claim
with zero counts remains unknown; a present Swarm Service intentionally scaled
to zero can converge. Inventory polling epochs do not change a stable target
baseline. A stale running-operation heartbeat is unknown. An operation
whose successful revision has been superseded does not confirm the new
deployment. Intentional stop/removal operations have no access requirement.
For Compose, native readiness counts inspected running containers with no
healthcheck or a healthy healthcheck. Gateway probes and client HTTP reachability
are separate observations.
Managed Compose convergence additionally requires its observed container count
to match the effective requested replicas from the immutable resolved revision,
saved application overrides or the selected service's explicit scale request.
Fewer surviving containers and leftover containers after scale-down remain
pending even when every observed container is healthy. Explicit temporary scale
operations on Swarm also require the native desired count to match their saved
request; ordinary Swarm workloads retain native desired-count semantics. Missing,
unreadable or malformed required intent remains unknown, and requested counts
form part of the stable target baseline. A zero-count absent Compose claim remains
unknown; intentional stop/removal and a present native Swarm zero remain separate
cases.
Binding retries retain the successful workload result and the original failure
history; they do not rerun every service.

During protected recovery, the global barrier, an operation-specific deployment
hold or Controller inspection mode makes every stage after acceptance and every
Gateway target unknown. Target identity remains visible; the recorded operation
and issued certificate are not changed. An unavailable recovery-state read also
leaves these observations unknown.

After release, the latest `recovery-review` document's `created_at` is the
observation cutover. Runtime and selected service observations, Gateway heartbeat,
received and observed evidence, and its loading confirmation must follow that
cutover. A restored ACK within the ordinary freshness window does not establish
current loading. Valid issued certificate material remains a cryptographic fact
after release and does not require reissuance merely because it predates the
review. These checks perform no recovery, deployment or certificate mutations.

The response includes a `baseline_digest`, per-Gateway results and explicit
`access_targets` with domain/route identity, host, scheme, public port, path and
expected certificate fingerprint. The digest binds configuration and target
identity, recovery hold/mode and review cutover, and excludes heartbeat timestamps
and transient observation states.
Compare it before and after a read-only client access check so changed targets
cannot reuse an older result. Actual access must be checked from an intended
client network. The Controller does not persist client probe results or infer
client success from loaded configuration.

The shared Console panel fetches current evidence on entry, polls while mounted,
and refreshes after mutations through the existing inventory coordinator. If a
refresh fails, previous confirmations are displayed as unknown with a visible
explanation. Switching operations cancels the previous query; its late response
cannot populate the new operation. Observation refresh never overwrites editable
deployment drafts.

The loopback Faker supplies accepted-request evidence only; it has no real
workload or Gateway confirmations. Unit and browser fixtures verify stage
presentation, authorization, redaction, partial results, navigation and failure
handling. Real Docker, ACME and Gateway acceptance remain separate integration
checks.

## Verify from the actual client network

Run the read-only client tool from the same network as the intended users:

```sh
python3 scripts/verify-access.py \
  --api-url https://console.example.test --operation op_selected \
  --token-file /private/read-only-token --vantage office-network \
  --output /private/new-access-report.json
```

Use an authorized existing read-only token stored in a private regular file
(mode `0600`). The tool never sends that credential to a business endpoint and
rejects API redirects. API HTTP requires explicit `--allow-http-api` for an
operator-controlled exception. Supply `--api-ca-file` and `--ca-file` separately
when private API and business trust roots are required.

The tool reads readiness before and after the probes. It resolves addresses
using the actual client resolver, bounds resolver execution, probes every answer
with the original Host/SNI, verifies the TLS chain and expected leaf fingerprint,
and requires a successful HTTP response. It does not retain response bodies,
application headers or cookies. Each DNS lookup, complete API read and transport
attempt to one resolved address has its own wall-clock timeout. Partial response
progress does not extend that deadline. Multiple addresses or targets can make
the overall run exceed one timeout interval.

Changed operation/configuration baselines or unavailable final observations
remain `UNKNOWN`. Individual successful probes remain visible. All platform
prerequisites must be current before the overall result can be `CONFIRMED`;
client reachability alone cannot prove loaded Gateway configuration. A CDN/LB
terminating TLS requires the explicit `--expect-edge-fingerprint` option: the
report identifies edge TLS and leaves the origin certificate `NOT_VERIFIED`.

Reports are created exclusively with mode `0600`, include the client network
label, operation baseline, checked addresses and verifier digest, and are never
uploaded or persisted in the platform. Their hosts/addresses are installation
information: keep them outside Git and public build artifacts. This is an
instantaneous GET reachability check, not a business transaction, load test or
continuous monitoring result.
