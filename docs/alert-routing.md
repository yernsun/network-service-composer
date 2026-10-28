# Alert evidence and delivery

NSC records persistent resource problems independently of outbound notification
configuration. An issue identifies a resource, an occurrence, its observation
state and the affected project/environment identities. Acknowledgement records
who has seen a problem; it does not resolve it. Responsibility references an
existing enabled NSC user. Both changes use revision preconditions.

Runtime, certificate and Gateway notifications share their health predicates
with issue collection. A pending certificate, stale observation, disabled
resource, missing certificate expiry or failed collection cannot establish recovery. Previously confirmed
problems remain visible when their current evidence is unknown. Healthy initial
observations do not generate recovery notices.

Deployment-target loss requires a fresh complete Runtime inventory, the exact
applied observation epoch, a running managed application without pending work,
and an absent managed service that expects positive replicas. Stopped, down,
zero-replica and unmanaged workloads are not missing deployment targets. Missing
inventory remains unknown and cannot authorize deletion or compensation.

Event ownership comes from durable environment/application and domain/service
bindings. Shared Gateways and certificates can affect multiple environments.
Display names and caller-supplied scope fields are not ownership authority.
Payloads contain identity metadata rather than Compose, credentials, endpoint
URLs or raw dependency errors.

Receivers and routing rules are configured separately. Each environment selects
its first matching enabled environment, project or global rule. A rule may have
several enabled receivers; disabled receivers allow parent fallback. Event group
and severity filters participate in matching. Shared events deduplicate by
receiver and contain only that receiver's affected environments. Configuration
activation does not replay historical activity. Active issues can notify newly
added targets once per occurrence.

The routing approach follows the first-match and parent-fallback ideas in the
[Alertmanager routing documentation](https://github.com/prometheus/alertmanager/blob/main/docs/configuration.md#route).
NSC implements fixed ownership scopes rather than an arbitrary label routing tree.
Receiver HTTP acceptance is not human acknowledgement. Control-plane recovery
does not restore application databases or volumes.

See [delivery operations](notification-delivery.md) for queue semantics, silence,
legacy compatibility, recovery holds and the administrator API.
