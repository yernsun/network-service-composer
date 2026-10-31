# Bootstrap and system maintenance

The installation executor runs independently of Controller tasks. Each installation
has one durable identity, one maintenance host and one private journal directory.
Copying checkpoints to another host does not authorize taking over a writer.
The product version remains unchanged.

## Supported installation contract

Linux Docker Engine must already be installed. Both amd64 and arm64 are supported;
images must match the node platform. A new single-node installation has one
Manager, Controller, production Gateway, PostgreSQL and dedicated Sandbox. New
multi-node installations select three Managers. PostgreSQL and Sandbox remain
pinned to one data node and volume. Controller/Gateway redundancy is **not full
HA**: database, host, zone and external ingress failures need separate recovery.

SSH identities must already be trusted in `known_hosts`. Use private key files or
an existing SSH agent, with root or noninteractive sudo. The installer never turns
off host verification, changes Docker daemon permissions, leaves a foreign Swarm,
or replaces a Worker entry with a Manager. Docker socket groups must match across
Controller nodes and permit non-root access.

## CLI workflow

Run the executor on its selected maintenance node. Protect the configuration and
state directory from other users. Guided installation is available with:

```sh
nsc-controller bootstrap init --interactive --state-dir /var/lib/nsc-bootstrap
```

For unattended setup, create a private configuration such as:

```yaml
mode: single
name: nsc
maintenance_node: node-1
data_node: node-1
node_dir: /var/lib/nsc
console_port: 8443
known_hosts_file: /etc/nsc-bootstrap/known_hosts
nodes:
  - name: node-1
    address: 192.0.2.10
    ssh_user: operator
    ssh_port: 22
    ssh_key_file: /etc/nsc-bootstrap/ssh-key
    manager: true
images:
  controller: registry.example.test/nsc/controller@sha256:REPLACE_WITH_VERIFIED_DIGEST
  gateway: registry.example.test/nsc/gateway@sha256:REPLACE_WITH_VERIFIED_DIGEST
  postgresql: registry.example.test/postgres@sha256:REPLACE_WITH_VERIFIED_DIGEST
# Optional private references; values never belong in this configuration.
# admin_password_file: /etc/nsc-bootstrap/administrator-password
# registry_auth_file: /etc/nsc-bootstrap/registry-config.json
```

Example addresses and placeholder digests are not deployable defaults. Supply
verified Controller/Gateway bootstrap-contract 1, protocol 2, schema 18 images,
and PostgreSQL 18 Alpine. Registry authentication uses a private Docker `auths`
configuration, transmitted through SSH stdin into a temporary private directory.
Credential helpers are not executed by the installer.

```sh
nsc-controller bootstrap init --config /etc/nsc-bootstrap/install.yaml \
  --state-dir /var/lib/nsc-bootstrap
nsc-controller bootstrap init --config /etc/nsc-bootstrap/install.yaml \
  --state-dir /var/lib/nsc-bootstrap --plan /var/lib/nsc-bootstrap/plan.json \
  --approve PLAN_SHA256 --idempotency-key UNIQUE_REQUEST_KEY
nsc-controller bootstrap status --state-dir /var/lib/nsc-bootstrap
nsc-controller bootstrap verify --config /etc/nsc-bootstrap/install.yaml \
  --state-dir /var/lib/nsc-bootstrap
nsc-controller bootstrap resume --config /etc/nsc-bootstrap/install.yaml \
  --state-dir /var/lib/nsc-bootstrap --operation RECORDED_OPERATION_ID
```

Review the exact plan: nodes, immutable images, cluster, database pin, downtime
and retention implications. Approval expires after ten minutes and binds the
installation revision, configuration and current observed resource versions. A
changed or expired plan must be regenerated. Reusing a request key with another
plan fails. There is no generic confirmation bypass.

## Installation and access

The installer pulls and checks image compatibility before creating identities.
It prepares or verifies Swarm, reserves the real NSC Service IDs without starting
tasks, distributes independent runtime identities, starts PostgreSQL, migrates
once offline and seeds stable administrator, Pool, Runtime and certificate IDs.
All running Controllers have `auto_migrate=false`.

The existing authenticated reconciler creates the protected Gateway project,
`prod` and `sandbox` environments and four system applications. The Sandbox has
an isolated network and no published ports. Installation requires a fresh
Controller/Manager observation, a real Sandbox NGINX validation, positive loaded
Gateway revisions with matching digests, administrator login and actual access.
Task RUNNING, version zero and a lone heartbeat do not establish completion.

The independent Controller HTTPS listener defaults to port 8443 and does not
need a domain or working Gateway. Its certificate contains inventory IP SANs
and defaults to twenty calendar years. The handoff prints console URLs,
certificate SHA-256 and private recovery/administrator file locations, never the
password. Verify the fingerprint through a trusted channel. DNS Domains still
require DNS names; IP SANs are certificate assets rather than Domain records.

An operator who has rotated the administrator password can pass a private current
credential using `--credential-file`. This transient input does not rewrite the
original recovery material or reset the administrator. Retain original material
only as protected historical recovery evidence; it is not proof of a valid login.

## Failure and recovery boundaries

Operations use PENDING, RUNNING, SUCCEEDED, BLOCKED, FAILED and UNKNOWN. Intent is
saved before a change and actual receipts before advancing. A lost response is
UNKNOWN even if Docker may have completed the write. Resume first performs
read-only reconciliation. A matching name or label cannot replace a creation
receipt. Completed material creation has a durable digest; partial or modified
private identities remain blocked rather than being regenerated.

Node-local configuration, Engine and Swarm identities and service versions are
checked before continuation. External changes require review. Failed installation
retains its database, volume, identities, operation history and private diagnostics.
There is no implicit uninstall or empty-volume replacement on another node.

A private PostgreSQL custom-format archive is checked with `pg_restore --list`
before Controller handoff. Keep the archive, matching encryption/session keys,
installation record and identity material together, offline and protected. This
is an initial database recovery point, not scheduled backup or application-volume
backup. A later system maintenance operation needs its own current backup.
Restoring database/business data is separate from image or configuration rollback.
The local archive limit is 1 GiB; a larger database requires a separately verified
backup procedure before maintenance.

Runtime mTLS identities are independent and the offline CA key is never copied
into running services. Runtime leaves have their own expiry, independent of the
twenty-year console certificate. Keep offline identity material available for
controlled renewal; losing a private key does not authorize minting a replacement
identity during repair.

## Verification and delivery status

The foundation journal/executor stage has an independent local commit. The
single-node adapter, HTTPS/IP certificate support and isolated SSH/Docker fixture
have passed local bootstrap, login, loaded NGINX, certificate, browser and archive
restore checks. Run `scripts/bootstrap_acceptance.py --browser` with source-bound
Controller and Gateway images to reproduce them. Multi-node adoption and web
maintenance are later stages of this change; their command names are reserved,
not a claim of completed acceptance. OVH DNS verification remains DISABLED.

The disposable acceptance fixture never uses the host Swarm. It verifies actual
PostgreSQL, SSH, Docker, NSC and NGINX in owned nested nodes. Nested nodes share a
physical host, so their results do not establish host or zone resilience. See
[Docker's Manager guidance](https://docs.docker.com/engine/swarm/admin_guide/)
and [Secrets documentation](https://docs.docker.com/engine/swarm/secrets/).
