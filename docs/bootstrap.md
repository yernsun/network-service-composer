# Bootstrap and system maintenance

NSC's installation executor is independent of the running Controller. An
installation has one durable identity, one maintenance host and one private
journal directory. A checkpoint copied to another host is diagnostic evidence,
not permission to take over an unfinished operation.

## Safety contract

Changes require an expiring plan bound to the installation revision, immutable
configuration and current external resource versions. Noninteractive execution
must approve that exact plan digest. Idempotency keys cannot be reused for a
different plan. The executor writes its intent before an external change and
persists the actual resource receipt before advancing.

Operations use PENDING, RUNNING, SUCCEEDED, BLOCKED, FAILED and UNKNOWN. A lost
response is UNKNOWN even when Docker may have completed the change. Resume first
checks actual resource identity and configuration. Missing creation receipts
cannot be reconstructed by claiming a matching name or label. Repair never
deletes retained data, regenerates a missing private identity, or overwrites
unreviewed external changes.

The first release supports a pinned, single PostgreSQL data node and volume.
Multiple Controllers and Gateways provide component redundancy; they do not
establish database failover or full high availability. Swarm manager quorum is
reported from the existing topology. New multi-node installations select three
Managers. See [Docker's manager guidance](https://docs.docker.com/engine/swarm/admin_guide/).

SSH host identities must already be trusted in a known-hosts file. Credentials
are private file references or existing SSH-agent identities. Runtime services
never receive the offline CA private key. Failed installation retains its
database, volumes, credentials and journal for inspection and recovery.

## Delivery status

The journal, plan validation and independent executor are implemented first.
Deployment adapters, CLI, HTTPS console and web maintenance are tracked by the
following implementation stages; this document must not be used as acceptance
evidence for stages that have not yet been verified.
