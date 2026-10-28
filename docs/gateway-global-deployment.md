# Gateway deployment guide

A global Gateway service runs on each eligible Swarm node. Prepare the deployment
from [the Agent configuration example](../config/agent.yaml) and the installation's
pool, network and node bindings. Public documentation uses placeholders; actual
service inspections and rollout observations belong in restricted local reports.

## Identity and state

Provision a distinct Gateway identity and mutual TLS credentials for each node.
A Gateway ID must correspond to the enrolled node and intended pool. Never use
an arbitrary hostname, a shared service VIP or a copied deployment export as
identity evidence. Keep private keys in external Secrets or operator-owned files.

Each Gateway needs its own writable generation and recovery state. Decide whether
state is persistent across task replacement or isolated per task before choosing
an update order. Concurrent tasks must not write the same `current` link,
`state.json`, PID file or generated NGINX configuration.

Use [the generic Compose definition](../compose.yaml) to review ports, protocol
publication, runtime privileges and state mounts. Adapt it to the authorized
installation. Keep the actual node IDs, Secret IDs, network IDs and host paths
out of the repository.

## Rolling updates

Follow [the Swarm rollout guide](gateway-swarm-rollout.md). Retain the existing
healthcheck, placement, network and Secret references when changing an image.
A rolling update must establish the intended image and current loading evidence
on every eligible node, with one final task per node. Preserve HTTP, HTTPS and
HTTP/3 publication according to the installation's existing configuration.

Run applicable [isolated verification](development.md) before deployment. After
rollout, compare desired and applied revisions, model/rendered digests and actual
route responses. Retain failed and unknown observations; neither task startup
nor a certificate's issued status proves that NGINX loaded the intended version.
Keep rollback artifacts locally and share only reviewed, sanitized summaries.

For the standalone global-service stack, local readiness command, capacity-checked
image upgrade script, and real multi-node fixture, see [NSC Swarm upgrades](nsc-swarm-upgrade.md).
