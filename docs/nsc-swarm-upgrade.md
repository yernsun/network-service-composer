# NSC rolling image upgrades on Swarm

The [Swarm stack](../deploy/swarm.yaml) deploys separate global Controller and
Gateway services on explicitly labeled nodes. It is independent of Compose
profiles. This procedure targets continuous HTTP/HTTPS business requests with
at least two healthy Gateways in one Pool. Controller election and API/gRPC
reconnection may be transiently unavailable; a verified Gateway LKG continues
serving without a Controller connection.

Support is conditional on the real multi-node acceptance result for the selected
images and Docker Engine. A task in RUNNING, a successful update command, or a
container health response alone is not a business availability guarantee.

## Provisioning and first migration

Use three Swarm managers in separate failure domains, at least two Gateway nodes,
and an externally operated single-writer PostgreSQL/MySQL endpoint. Three
Gateway nodes leave capacity for rollback after one candidate fails. SQLite and
embedded Gateways are outside this deployment. Provision infrastructure, database
migrations, and existing Compose-to-Swarm traffic migration separately; first
installation and topology migration are not rolling-upgrade acceptance windows.

On each selected node, create an absolute, operator-owned directory such as
`/srv/nsc` containing:

- `controller.yaml` and `controller-secrets/`: a unique Controller instance ID,
  certificate, shared keyring/session key, external database DSN, and exact
  provisioned node/engine/cluster bindings where cluster management is enabled.
- `agent.yaml` and `gateway-secrets/`: a distinct enrolled Gateway ID and mTLS
  certificate for the intended Pool, database leader-discovery configuration,
  and `nginx.generation_dir: /var/lib/nsc/generations`.
- `gateway-state/`: local durable storage owned by UID 101. Never share this
  directory over a shared filesystem or between different Gateway identities.
- `gateway-cache/`: local scratch storage owned by UID 101 for NGINX temporary
  request/proxy files. It is separate from durable generation state.

Start with [Controller configuration](../config/controller.yaml) and
[Agent configuration](../config/agent.yaml). The stack overrides the Gateway's
main configuration path to `/var/lib/nsc/nginx.conf`; keep it stable across
replacements. Existing state from another main-config layout needs a separately
verified migration. Use UID 65532-readable Controller files and UID 101-readable
Gateway files; do not make private keys world-readable. Configure secret file
references rather than embedding secret values in deployment environment exports.
All selected Controller nodes must expose the Docker socket with the configured
`NSC_DOCKER_GID`.

Each Controller advertises its own reachable private node address on 9444 and
9443, covered by its certificate. Do not advertise the shared service VIP as an
individual Controller identity. Those ports use host publication: firewall them
to the trusted control network. Console port 8080 uses ingress; internal metrics
are not published. Disable Controller automatic migrations and local Gateways,
as enforced by the stack; run compatible migrations once before admitting peers.

Label only fully provisioned nodes with `nsc.controller=true` and/or
`nsc.gateway=true`. Create the intended external application overlay in advance.
Distribute images or provide registry access to every eligible node. Export:

```sh
export NSC_CONTROLLER_IMAGE='registry.example.test/nsc/controller@sha256:<digest>'
export NSC_GATEWAY_IMAGE='registry.example.test/nsc/gateway@sha256:<digest>'
export NSC_NODE_DIR=/srv/nsc
export NSC_DOCKER_GID='<socket-group-id>'
export NSC_INGRESS_NETWORK=nsc-ingress

docker stack config -c deploy/swarm.yaml
docker stack deploy -c deploy/swarm.yaml nsc
```

The example uses the same absolute provisioning path on each node, with different
contents and private state. Register all Gateways, publish a valid Pool generation,
and verify all nodes before starting an upgrade. Bootstrap revision zero is
intentionally unhealthy. Healthcheck startup grace allows initialization; it
does not admit an empty Gateway to ingress.

## Local health and update policy

`nsc-agent --healthcheck --config /etc/nsc/agent.yaml` checks persisted Pool and
revision metadata, rendered/disk agreement, the loaded NGINX generation, and
`nginx -t` next-start validity. It does not run Prepare, modify generation files,
connect to the database, or require a Controller. Transient concurrent generation
changes fail the sample conservatively. Docker's retry policy absorbs individual
transient observations. Diagnostics use bounded machine codes without raw NGINX
output or credentials.

Both services use `parallelism: 1`, `stop-first`, 30-second delay/monitor,
`max_failure_ratio: 0`, and `failure_action: pause`. Rollback has the same policy.
The 30-second stop grace permits the Agent's existing 25-second SIGQUIT shutdown.
State and identity are reused only after the previous task exits. HTTP/HTTPS and
UDP 443 remain ingress publications; UDP preservation is not HTTP/3 acceptance.

## Upgrade command

Run [the upgrade script](../scripts/swarm_rollout.py) on the intended manager.
It uses the explicit local Unix socket and Docker Engine API service-version
preconditions. It never builds images, creates services, enrolls identities,
changes deployment policy, migrates databases, or rolls back automatically.

Create a private JSON configuration (all identifiers are installation inputs):

```json
{
  "docker_socket": "/var/run/docker.sock",
  "controller_service": "nsc_controller",
  "gateway_service": "nsc_gateway",
  "api_url": "https://console.example.test",
  "token_file": "/run/operator/nsc-token",
  "api_ca_file": "/run/operator/control-ca.crt",
  "gateways": [
    {"node_id": "node-a-id", "gateway_id": "gw_a", "pool_id": "pool_a"},
    {"node_id": "node-b-id", "gateway_id": "gw_b", "pool_id": "pool_a"}
  ],
  "probes": [
    {"node_id": "node-a-id", "url": "http://app.example.test/health", "connect_address": "192.0.2.10", "body_sha256": "<64 hex characters>"},
    {"node_id": "node-a-id", "url": "https://app.example.test/health", "connect_address": "192.0.2.10", "body_sha256": "<64 hex characters>"},
    {"node_id": "node-b-id", "url": "http://app.example.test/health", "connect_address": "192.0.2.11", "body_sha256": "<64 hex characters>"},
    {"node_id": "node-b-id", "url": "https://app.example.test/health", "connect_address": "192.0.2.11", "body_sha256": "<64 hex characters>"}
  ]
}
```

The node-to-Gateway mapping is operator-provisioned input, not inferred from a
hostname. Supply HTTP and HTTPS probes for every Gateway node. Use a deterministic
read-only route; each probe checks status (default 200), exact response SHA-256,
and TLS trust/hostname. `connect_address` selects a node while preserving Host and
SNI. Optional per-probe `ca_file` supplies a private trust root. Redirects are not
followed, and response bodies are limited to 1 MiB and never written to reports.

```sh
python3 scripts/swarm_rollout.py check --config /run/operator/rollout.json \
  --report-dir coverage/swarm-upgrade/run-001
python3 scripts/swarm_rollout.py upgrade --config /run/operator/rollout.json \
  --report-dir coverage/swarm-upgrade/run-001 \
  --controller-image 'registry.example.test/nsc/controller@sha256:<digest>' \
  --gateway-image 'registry.example.test/nsc/gateway@sha256:<digest>'
```

Only verified schema/protocol-compatible image pairs are supported. No product
version is advanced by this mechanism. Controller completion and fresh Gateway
loaded evidence precede Gateway submission. Unsupported placement expressions,
in-flight changes, unhealthy nodes, incomplete mappings, mutable images, and
insufficient healthy capacity fail admission. Supported placement constraints
are equality/inequality on node ID, role, and labels.

The script persists intent before mutation, changes only the image, and refuses
stale service versions. Continuous per-node HTTP/TLS samples accompany updates.
The default wait deadline is 900 seconds per service (`--timeout`). A traffic
failure remains recorded even when the scheduler completes. Concurrent operator
changes stop observation with a conflict; no full-spec restoration is attempted.

## Paused updates, unknown results, and rollback

A rejected candidate pauses the Swarm update, preserving the other tasks. Fix
image availability or restore healthy capacity before requesting recovery.
Two healthy Gateways are required before rollback, so a two-node installation
with one failed Gateway cannot immediately roll back through this helper. Restore
capacity using the installation's separately reviewed recovery procedure; do not
remove or restart its only serving Gateway.

```sh
python3 scripts/swarm_rollout.py status --config /run/operator/rollout.json \
  --report-dir coverage/swarm-upgrade/run-001
python3 scripts/swarm_rollout.py rollback --component gateway \
  --config /run/operator/rollout.json --report-dir coverage/swarm-upgrade/run-001
```

Rollback requires the recorded latest upgrade and an exact matching PreviousSpec,
including unchanged non-image settings. It uses Swarm's rollback policy; failure
pauses again. It restores neither the database nor business data. Retain old
images, compatible schema, credentials, and state until recovery is verified.

Timeout, interrupted observation, or lost Engine access produces UNKNOWN, never
success. Ending the script does not cancel Swarm work. `status` re-reads service
IDs, intended specs, tasks, and current loaded evidence; a completed Controller
phase does not imply the unsubmitted Gateway phase completed. Reports retain
historical failures and cannot turn failed traffic acceptance into a clean pass.
Private reports use mode 0600 beneath a mode 0700 directory. Keep reports and
provisioning files out of Git.

## Acceptance

```sh
make test-swarm-rollout-unit
make test-swarm-rollout NSC_CONTROLLER_IMAGE='<built-current-controller>' \
  NSC_GATEWAY_IMAGE='<built-current-gateway>'
```

The fixture uses three nested Docker daemons, real NSC services, real NGINX,
isolated PostgreSQL, and an isolated registry with immutable image variants. It
never joins or modifies the host Swarm. Required images from `build/image-lock.json`
must already exist. Every normal/recovery window requires at least ten samples
per node per protocol and zero failed samples. Scenarios cover Controller election,
serial Gateway image replacement, delayed readiness, persistent LKG, bad-image
pause, and capacity-checked rollback. Normal upgrades start with two Gateways;
the third is provisioned before fault injection to establish rollback capacity.
Raw request samples and failure evidence
remain under the chosen ignored report directory.

Run backend coverage, the full harness, applicable race checks and real NGINX
integration separately. The old ordinary-NGINX Swarm test remains a scheduling
fixture. Neither fixture establishes production host/zone resilience or arbitrary
cross-version compatibility. Record the actual source fingerprint, images,
results, and missing prerequisites before claiming acceptance.
