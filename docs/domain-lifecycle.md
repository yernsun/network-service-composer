# Domain, DNS and certificate lifecycle

## Ownership and scopes

A Project contains Environments. An Environment chooses one Runtime, whose Gateway Pool owns the live Domain and Route records. A Service belongs to an Environment through its application claim. A Domain can serve several Routes and Services in one Pool; the Gateway targets are Pool members, not a per-domain selection. The Console keeps the Environment of a newly published Domain in its domain-draft receipt, while older Domains without a receipt derive their visible scope from bound Services.

## Draft and publication

Saving a domain draft writes only an Environment-scoped `domain-draft` control document. It does not insert a live Domain, Route or Certificate, change a Gateway Pool revision, create public DNS records, or request a certificate. The draft can be edited or deleted with `If-Match` revision checks. A draft without a Service may be saved but cannot be published.

Publishing revalidates the Environment, Service ownership, TLS selection and enabled Gateway DNS targets. One database transaction creates the Domain, initial Route, pending ACME Certificate when appropriate, deployment operation for managed Services, and a published draft receipt. It also advances the Gateway Pool desired revision. A stale draft revision fails without applying part of that transaction. The receipt records the Domain, Route and operation IDs for audit and retry. The operation and subsequent Gateway applied revision are the publication evidence; the draft receipt alone does not claim that every Gateway has loaded the route.

## TLS and DNS

- **Managed DNS verification** selects ACME DNS-01 and a reusable DNS provider profile. The Controller creates and removes only ACME TXT challenge records. Provider identifiers include Cloudflare, HE.net/Hurricane, Aliyun/Alidns, Tencent Cloud and OVH; OVH-specific verification remains temporarily **disabled** and is not tested acceptance. DNS profiles may also be supplied through the existing read-only Controller configuration.
- **Non-managed DNS verification** selects ACME HTTP-01. It has no DNS provider reference. The Controller still manages certificate issuance and renewal; public DNS and HTTP reachability must be configured by the operator.
- **HTTP only** has no TLS certificate or ACME challenge.
- **Custom certificate** links an already imported certificate that covers the host.

Public A, AAAA and CNAME records remain the operator's responsibility in every mode. Certificate status, expiry and error come from Certificate records. Gateway status and applied configuration revision are separate deployment observations.

Administrator-created DNS profiles are global and reusable across Projects and Environments. Their credentials are encrypted in the control database and never returned by API reads; the Controller decrypts profiles internally for metadata validation and ACME use. Creation, replacement and deletion require administrator reauthentication. Replacement and deletion require the current ETag. Existing config-backed profiles remain read-only and take precedence over the same database reference without removing it. A profile referenced by a Domain, Certificate or unpublished draft cannot be deleted. An unavailable profile fails issuance rather than changing the certificate's challenge method. See [DNS-01 configuration](configuration.md#acme-dns-01) for replacement and [Security](security.md#secret-encryption-and-dns-credentials) for keyring recovery requirements.
