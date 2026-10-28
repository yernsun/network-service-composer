# NSC v0.1.0 community candidate

Network Service Composer for Nginx brings the existing trusted-team Compose/Swarm
management plane into a fresh-history community repository. It includes projects
and environments, reviewed application deployment, NGINX gateway publication,
certificate assets, image update policies, signed Webhook notifications and activity
records. Self-signed certificates default to 20 years; clients must explicitly trust
them. Eight interface locales are retained.

The candidate strengthens gateway probe ownership, upstream TLS authentication,
public-only outbound delivery and scoped automation storage permissions. Previously
reviewed scoped deployments must be reviewed again. Internal HTTPS upstreams require
a trusted CA bundle. Upgrade Controller and Gateway together for the renderer change.

The product version is v0.1.0; database migrations and protocol identities retain
their existing sequence. This is not evidence of a tested cross-version production
upgrade. Application rollback does not reverse database migrations or restore user
volumes. OVH live verification remains disabled.

Read the [release contract and blockers](community-release.md),
[security model](security.md), [contribution guide](../CONTRIBUTING.md) and
[third-party notices](../THIRD_PARTY_NOTICES.md). A candidate is publishable only
after its exact source and artifacts pass every required admission gate.
