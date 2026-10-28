# Security policy

The v0.1.0 community candidate targets trusted teams operating Compose and
Swarm. It is not a hostile multi-tenant container sandbox. The controller's
Docker authority and database credentials are privileged. Restrict management
endpoints, use HTTPS, and protect configuration, database backups and encryption
keys together. See [deployment security](docs/security.md).

## Reporting vulnerabilities

Use the repository's private vulnerability reporting form under **Security →
Report a vulnerability** when available. Do not publish exploit details, private
keys, production addresses, database contents or raw operational logs in public
issues. If private reporting has not been enabled in the new repository, first
open a public issue requesting a private contact channel without sensitive detail.
Enabling private reporting is a release-owner prerequisite; this document does
not assert that the new repository is already configured.

Include affected versions, the violated authorization or ownership boundary,
minimal synthetic reproduction, expected/actual behavior and mitigations.
Maintainers will coordinate validation and disclosure; no response-time SLA is
promised for the first community release.

## Scope and support

Security fixes target the latest community release. Until v0.1.0 passes the
release gates it remains a candidate, not a supported production release. Report
cross-project/pool mutation, credential exposure, unauthorized host access,
outbound policy bypass, stale executor writes and false success evidence.
Experimental capabilities have the same confidentiality and data-safety duties.
Deleting leaked material does not revoke credentials: owners must rotate/revoke
them and address existing public copies separately.
