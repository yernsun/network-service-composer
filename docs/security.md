# Security model

## Management plane

- `/api/v1` supports constant-time Bearer token comparison and browser Cookie sessions. An explicitly invalid Authorization header never falls back to Cookie authentication.
- On first startup, the configured administrator username and token are imported once as the initial password and API token. Passwords use bcrypt; tokens use SHA-256. Manage passwords and account disabling in the account and user pages; restarts do not reimport old credentials. Login issues signed HttpOnly, SameSite=Strict cookies. Cookie writes require CSRF protection (`X-CSRF-Token` for JSON APIs). Session restoration returns only an in-memory CSRF token, never the administrator password.
- Community provides administrator and global viewer roles. The backend rejects viewer console resource writes and user-directory access. User changes and token rotation require the current password through `X-NSC-Reauthenticate`. Account revision changes invalidate old cookies; password changes and resets revoke the legacy user Bearer token while retaining project API keys. Browser-initiated token rotation returns a new session and CSRF token. Roles do not provide environment/team authorization.
- DevOps OpenAPI keys are separate from user Bearer tokens and console sessions. Legacy keys remain environment-scoped and deployment-only; user-owned project keys carry explicit DevOps scopes and cover future environments in one project. Key issuance and revocation require password reauthentication, and revocation requires an ETag. The API accepts only `X-NSC-API-Key`, checks user and resource ownership on every request, and uses immutable operation scope snapshots for project-key history reads. Keys are stored as SHA-256 digests and returned once. Disabling a user or changing their role permanently revokes their keys; password changes retain project keys. See [DevOps OpenAPI](openapi-deployments.md).
- SPA entry pages are uncached; content-hashed assets use long-term caching. PrimeVue styles use a per-response nonce; script CSP permits neither inline scripts nor eval.
- PATCH/DELETE require resource ETags to prevent concurrent overwrites.
- Request bodies, request rates and gRPC snapshot sizes are bounded.
- `/healthz`, minimal `/readyz` and `/.well-known/acme-challenge/*` allow anonymous access. Compose publishes Prometheus to host loopback by default; the native process listens on the configured metrics address, which can include all interfaces. Apply network restrictions for native and peer-container access.
- API/UI writes produce structured audit events. Certificate PEM private-key fields use `json:"-"`. Audit stores actor, action, resource and request SHA256, never request bodies, Compose, environment values or snapshots.

Activity and issue pages retain operations and unresolved problems. Acknowledgement does not resolve an exception. Ordinary activity history does not guarantee tamper-proof compliance auditing.

## Data-plane identity

Agents require TLS 1.3 client certificates issued by the offline CA, containing a unique URI:

```text
spiffe://nsc.local/gateway-pools/<pool-id>/gateways/<gateway-id>
```

The Controller checks the chain, URI, Hello identity, Gateway database state and certificate SHA-256 pin established at registration. Setting Gateway `enabled` to false revokes connection eligibility.

Controller/Agent handshake capability version is 3; the protobuf package remains v2. The v0.1.0 community baseline requires rebuilding both sides together. Old Agents are rejected rather than assumed to support runtime evidence. Product version reset does not reset schema migrations or protocol versions.

Keep the CA private key in offline `pki/`; Compose does not mount it. Controller and Gateway secrets use separate directories to avoid exposing one component's private keys to another.

## Secret encryption and DNS credentials

ACME account keys, TLS private keys, Application source Compose, normalized environment, resolved Compose and recoverable Operation inputs use AES-256-GCM envelopes. Envelopes contain a key ID, random nonce and ciphertext. Resource ID and field name are authenticated as AAD to prevent ciphertext substitution across rows/fields. Decryption failure aborts preflight, deployment or rollback rather than producing empty configuration.

DNS-01 has two profile sources. Administrator-created profiles accept write-only credentials through `/api/v1/dns-provider-profiles` and store the profile in an AES-256-GCM encrypted control document. Creation, replacement and deletion require the administrator's current password in `X-NSC-Reauthenticate`; replacement and deletion also require the current `If-Match` ETag. Global viewers and project automation keys cannot write these console profiles. Profile reads and Settings return only metadata and references, never credentials. The Controller decrypts database profiles internally for metadata validation and ACME use; they are not sent to Gateways, configuration snapshots or audit records.

Configuration-backed profiles load credentials from read-only JSON files at startup. A configured reference takes precedence over a database profile with the same reference and cannot be changed or deleted through the API. The shadowed database profile remains encrypted and becomes active again if the configuration entry is removed and the Controller restarts. Keep every HA Controller's configuration and secret files equivalent to avoid different credentials for the same reference.

Restrict DNS identities to the required zone/TXT permissions and keep them separate from human administrator credentials. API credential writes require a trusted HTTPS management connection. Credentials necessarily enter the submitting browser or client; do not save request bodies, credentials or reauthentication passwords in browser persistence, shell history, access logs or ordinary diagnostics. A database backup contains encrypted database profiles and requires its matching keyring; configuration-backed credentials require separate protected file backups. See [DNS-01 configuration](configuration.md#acme-dns-01) for credential replacement and source selection.

Rotation procedure:

1. Add a new 32-byte Base64 key to `NSC_SECURITY__ENCRYPTION_KEYS`, retaining the old key.
2. Set `NSC_SECURITY__PRIMARY_KEY_ID` to the new ID and roll the Controllers.
3. Stop all Controllers (Gateways may continue using LKG) and run `nsc-controller keys reencrypt --config /etc/nsc/controller.yaml`. This reencrypts certificate keys, ACME account keys, Application revisions, durable Operation inputs and encrypted control documents, including DNS profiles, variable history, source drafts, release/restore records, node reports and hook secrets. Configuration-backed credential files are not rewritten.
4. Check the command's results and verify the new key identity on every encrypted resource category, including DNS profile control-document envelopes. Test decryption with the new keyring in an isolated restore before removing the old key in the next maintenance window. If any category fails, retain both keys and resolve the failure; the command may already have reencrypted earlier categories.
5. Retain older keys in protected offline backup storage for as long as backups encrypted with them remain recoverable. Removing a key from the live keyring does not reencrypt existing backup archives or revoke the provider's DNS credentials.

## Managed Compose safety boundary

Preflight allows image, environment, healthcheck, named volume, network, ports, depends_on and common deploy policies. Administrators may use validated absolute host bind paths on standalone Compose; Swarm persistence additionally follows the selected data-node policy. These mounts carry host-level authority and are not a tenant-isolation mechanism. Scoped project API keys cannot request host binds, globally named/external volumes or volume drivers. This restriction is frozen in the review and revision and checked again before execution. Previously scoped reviews without this restriction must be recreated. Builds, privileged mode, host PID/IPC/network namespaces, devices, Docker sockets, dangerous capabilities and local secret/config files remain rejected. Node-bound execution verifies local engine, node and cluster identity before mutation. External secrets/configs and private registries use administrator-configured references. Interpolation uses only template defaults and Application values, never the Controller process environment.

Remote catalog sources require explicitly trusted HTTPS URLs, reject redirects and environment proxies, and block localhost, private/link-local addresses and DNS rebinding. Legacy GIT indexes pin a full commit/digest; static manifest sources verify declared file hashes and confirmed endpoints. Compose and artifact hashes are checked during synchronization and installation.

## Container permissions

Controller and Gateway images run as non-root. The Compose deployment applies no-new-privileges, capability drops and a read-only Gateway root filesystem; the Swarm examples do not establish all the same controls. Gateway generation storage remains writable and contains private keys. Runtime state follows `nginx.generation_dir`; protect that directory and its backups. Docker socket access requires the host socket group through DOCKER_GID and grants extensive host privileges even when the socket mount is read-only. Prefer a restricted remote Docker API with mTLS credentials. Default Compose Controller and Gateway database credentials share a principal: mTLS separation does not imply database privilege separation.

## HTTPS upstream trust

All HTTPS proxy destinations verify their certificate chain and peer name. The
default CA bundle is `/etc/ssl/certs/ca-certificates.crt`. For an internal CA,
mount a combined trust bundle read-only on each Gateway and configure
`nginx.upstream_ca_file` (or `NSC_NGINX__UPSTREAM_CA_FILE`). The path is local to
each NGINX process; distribute identical trust policy to every serving Gateway.
Untrusted or wrong-name peers fail closed. A self-signed certificate issued by
NSC does not automatically become trusted by a browser or upstream client.
See the [NGINX proxy TLS directives](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_ssl_verify).
