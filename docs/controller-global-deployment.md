# Controller deployment guide

Deploy Controllers from a tested image built from the intended Git revision.
Keep the installation's service specifications, node bindings, image digests,
addresses and verification reports outside the source tree. The public examples
use placeholders and do not describe an existing installation.

## Configuration and identity

Use [the Controller configuration example](../config/controller.yaml) and
[configuration reference](configuration.md) to prepare operator-owned settings.
Controllers in an HA deployment share the same database and encryption key.
Provision a distinct Controller instance identity and mutual TLS material for
each node. Resolve node bindings through immutable Docker Engine and Swarm Node
IDs; hostnames are display metadata, not ownership evidence.

Supply configuration and private keys through restricted external Secrets or
operator-owned files. Store writable state in a dedicated volume or a path such
as `/srv/nsc/state`. Do not publish a Docker inspection export as a reusable
service definition: it embeds installation-specific IDs, names, environment
values, network references and mount paths.

Enable local Gateway or application management only for the intended Runtime
and node binding. Use [the generic Compose definition](../compose.yaml) as a
starting point, then review placement, networks, mounts and Secrets for the
installation. No example is authorization to replace an existing service.

## Console entry point

Expose the Console through an operator-owned reverse proxy and hostname, such
as `console.example.test`. The optional
[entrypoint Compose example](../deploy/controller-entrypoint.compose.yaml) uses
[the proxy configuration](../deploy/controller-entrypoint.conf). Choose an
available listener for the installation and preserve WebSocket forwarding.
Configure the Controller entrypoint settings only when NSC should own that
binding. Existing conflicting routes remain protected by the reconciler.

## Update and verification

Save the existing service specification and observations in a restricted local
report before updating. Inspect the current update and rollback policies rather
than assuming an example applies. Verify that the new image exists on every
eligible node before changing the service's image reference.

After the update, check each running Controller's image, binary version,
`/readyz` response and embedded Console assets. Verify peer ownership, exactly
one leader where HA coordination requires it, database schema compatibility,
Gateway connectivity and the existing application bindings. A command exit or
container health alone does not prove those outcomes.

Use [the engineering harness](development.md) and applicable isolated integration
fixtures for source acceptance. Keep actual deployment evidence under ignored
local reports; follow [repository privacy](repository-privacy.md) before sharing
any extract. Preserve the previous image and configuration until rollback has
been verified.

For the standalone global-service stack, local readiness command, capacity-checked
image upgrade script, and real multi-node fixture, see [NSC Swarm upgrades](nsc-swarm-upgrade.md).
