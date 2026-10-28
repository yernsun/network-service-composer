# Configuration

Controller and Agent use strict YAML; unknown fields fail startup. Supported fields can be explicitly overridden with NSC_ environment variables, using double underscores for nesting, such as NSC_STORAGE__DRIVER. See [controller.yaml](../config/controller.yaml) and [agent.yaml](../config/agent.yaml).

## Internationalization

The shared locale authority is internal/i18n/locales.json: English en, Japanese ja, German de, Simplified Chinese zh-CN, Traditional Chinese zh-TW, French fr, Russian ru and Spanish es. Set i18n.default_language or NSC_I18N__DEFAULT_LANGUAGE; unsupported values fail startup.

The UI language selector sets a one-year nsc_locale cookie. Request language precedence is that cookie, weighted BCP 47 Accept-Language, then the configured default. zh/zh-Hans/zh-SG map to zh-CN; zh-Hant/zh-HK/zh-MO map to zh-TW. Unsupported languages use the default. Responses include Content-Language and Vary: Accept-Language, Cookie.

REST errors use RFC 9457 with stable code, optional params and field-level errors[] as the machine contract. title/detail are localized display text and must not be parsed or matched by clients:

```json
{
  "type": "urn:nsc:problem:validation.dns_ref_required_dns01",
  "title": "Validation failed",
  "status": 400,
  "detail": "A DNS provider reference is required for the DNS-01 ACME challenge.",
  "code": "validation.dns_ref_required_dns01",
  "errors": [{"code": "validation.dns_ref_required_dns01", "pointer": "/tls/dns_provider_ref"}]
}
```

Resource enums, IDs, field names and UTC RFC3339 timestamps are not localized. Original Docker, NGINX, ACME and database diagnostics remain in their source language, shown only in technical details or structured logs.

## Secret boundaries

The following installation settings come from environment variables or read-only files rather than the resource API. Persisted local identities are managed separately as described in security.md:

- Initial administrator username: NSC_SECURITY__ADMIN_USERNAME, default admin.
- Initial administrator password/Bearer token: NSC_SECURITY__ADMIN_TOKEN or security.admin_token_file.
- UI session signing key: NSC_SECURITY__SESSION_KEY or security.session_key_file.
- AES-256-GCM keyring: NSC_SECURITY__ENCRYPTION_KEYS JSON, or a read-only JSON secret referenced by security.encryption_keys_file / NSC_SECURITY__ENCRYPTION_KEYS_FILE; values are standard Base64-encoded 32-byte keys.
- ACME DNS credentials: configuration-backed profiles use the read-only JSON file at acme.dns_providers.<ref>.credentials_file; administrator-created profiles accept write-only credentials through the console API and encrypt them in the database. Domains and Certificates store only the profile reference; profile reads and Settings never return credentials.
- Docker endpoints use SDK-native unix://, tcp:// or npipe:// transports. Configure remote TCP mTLS with docker_credentials.<ref>.{ca_file,cert_file,key_file}.
- Private registries reference administrator-configured registry_credentials.<ref>.config_file, a read-only Docker config.json. Credentials are absent from Application, Revision, Operation and Settings responses.
- Controller/Agent mTLS private key

Runtime records store credential_ref, never Docker TLS private keys. Settings redacts token/keyring values and does not return Docker credentials.

## Docker discovery and label DSL

Compose discovers non-one-off containers with com.docker.compose.project and com.docker.compose.service labels, including stopped containers. Swarm discovers all Services. Standalone and Compose one-off containers are excluded. nsc.enable=true enables label-created Routes, not visibility. Compose labels belong on the container service; Swarm labels belong under deploy.labels in the Service Spec.

| Label | Value | Meaning |
|---|---|---|
| `nsc.enable` | boolean | Enable label-created Routes |
| `nsc.port` | 1..65535 | Backend port; inferred only for one exposed TCP port |
| `nsc.protocol` | `http` / `https` | Default upstream protocol |
| `nsc.readiness.mode` | `auto/http/docker/tcp/grace` | Readiness mode |
| `nsc.readiness.path` | absolute path | HTTP probe path |
| `nsc.readiness.scheme` | `http` / `https` | HTTP probe scheme |
| `nsc.readiness.interval` | duration | Probe interval |
| `nsc.readiness.timeout` | duration | Probe timeout, no greater than interval |
| `nsc.readiness.success-threshold` | positive int | Consecutive successes |
| `nsc.readiness.failure-threshold` | positive int | Consecutive failures |
| `nsc.readiness.startup-grace-period` | duration | Running+Grace delay |
| `nsc.readiness.startup-timeout` | duration | Startup deadline |
| `nsc.readiness.tcp-fallback` | boolean | Whether auto mode may select TCP |

Each Route uses nsc.routes.<name>.*:

| Suffix | Meaning |
|---|---|
| `host` | Required lowercase exact IDNA hostname; no wildcard |
| `path` | Defaults to / and normalizes to a trailing slash |
| `entrypoints` | http, https or http,https; omitted means HTTP only |
| `redirect-http-to-https` | Requires both entrypoints |
| `tls` | disabled, managed or custom; HTTPS requires managed/custom |
| `port` | Override the service port |
| `protocol` | Override upstream HTTP/HTTPS |
| `server-name` | HTTPS upstream SNI |
| `priority` | Integer priority |
| `strip-path` | boolean |
| `websocket` | boolean |

Invalid labels disable the discovered configuration and expose reasons in Service/Route APIs, UI, logs and metrics. Identical Discovery host/path/priority claims are all disabled until resolved.

Service protocol_override, target_port_override, enabled_override and readiness_override are nullable: omitted leaves unchanged, a value overrides discovery, and null restores discovery behavior. Reconciliation changes discovery values without overwriting user overrides.

## Sources and matching

Source priority is SYSTEM > API > STATIC > DISCOVERY. Locations sort by longest path, source, priority and stable ID. The renderer emits SYSTEM ACME locations first. Same-level API conflicts return HTTP 409.

## Networking

Each Pool declares a shared backend network, default nsc-ingress. NGINX upstreams use Docker Service DNS only: project_service for Compose and full stack_service for Swarm. Short names, arbitrary API hostnames, container/Task IPs and literal VIPs cannot enter generated configuration. Duplicate DNS identities in one Pool fail closed on both sides. Recreation, Task IP changes and scaling update instance state without config revisions or reloads.

Managed Compose overrides attach the Pool network and project_service alias; managed Swarm uses full native Stack service names. External Compose replicas must supply the unique qualified alias, and external Swarm services must use qualified names and join the overlay; otherwise binding returns 422. Instance addresses are diagnostic only.

External Services remain read-only: NSC does not create/attach networks or modify Docker objects. Missing Pool networks produce actionable binding diagnostics. The first managed binding transaction generates a separate override and queues redeployment onto the existing Pool network without rewriting source Compose. Removing the last API Route removes only NSC's generated network attachment. NSC publishes no additional ports and deletes no named volumes or images.

## Application management switches

Docker mutations require both the following configuration:

```yaml
management:
  enabled: true
  external_secrets: [production_database_password]
  external_configs: [production_application_config]
```

and management_enabled: true on the target Runtime. Both switches default off. Settings reports global enablement but cannot edit it. SQLite supports Compose only; Swarm management requires PostgreSQL/MySQL and a manager endpoint.

management.external_secrets and management.external_configs allow existing named Docker resources referenced with external: true. Both default empty; JSON array overrides are NSC_MANAGEMENT__EXTERNAL_SECRETS and NSC_MANAGEMENT__EXTERNAL_CONFIGS. management.docker_cli selects the pinned CLI; execution uses argument arrays, never shell construction. work_dir holds task-scoped temporary files. poll_interval, heartbeat_interval, stale_after and deploy_timeout control queue polling, lease recovery and deadlines. See [application management](application-management.md).

Gateway DNS is fixed to `resolver 127.0.0.11 valid=5s ipv6=off;` and `resolver_timeout 2s;`, with no API/YAML/environment override. ACME challenge upstreams also resolve at runtime, allowing LKG cold starts while the Controller is unavailable.

## ACME DNS-01

HTTP-01 remains the managed TLS default. DNS-01 can use administrator-created database profiles or read-only configuration profiles. Both are global references reusable across Projects and Environments; this is not project-level credential isolation.

Manage database profiles through the DNS profile console or `/api/v1/dns-provider-profiles`. POST creates a profile; PUT replaces its complete provider, region and credentials, rather than retaining an omitted secret. DELETE removes an unused profile. All writes require an administrator and `X-NSC-Reauthenticate`; PUT and DELETE also require the current `If-Match` ETag. GET returns only `reference`, `provider`, optional `region`, `source`, `configured` and `revision`. A Domain, Certificate or unpublished draft using the reference blocks deletion. Database profiles expose provider and region only; the additional timing and endpoint settings below are configuration-backed options.

Alternatively, declare named configuration profiles containing the provider type, secret-file path and non-sensitive propagation settings:

```yaml
acme:
  directory_url: https://acme-v02.api.letsencrypt.org/directory
  email: ops@example.com
  accept_terms: true
  dns_providers:
    aliyun-prod:
      provider: alidns
      credentials_file: /run/nsc/alidns.json
      region: cn-hangzhou
      ttl: 600
    cloudflare-prod:
      provider: cloudflare
      credentials_file: /run/nsc/cloudflare.json
      ttl: 120
    tencent-prod:
      provider: tencentcloud
      credentials_file: /run/nsc/tencentcloud.json
      ttl: 600
    he-prod:
      provider: hurricane
      credentials_file: /run/nsc/hurricane.json
      propagation_timeout: 5m
```

Provider identifiers are alidns, cloudflare, tencentcloud, hurricane and ovh. OVH-specific verification is temporarily **disabled** by maintainer instruction and does not constitute tested provider acceptance. Optional configuration settings are region, base_url (Cloudflare-compatible API), ttl, propagation_timeout, polling_interval, http_timeout and sequence_interval (DNS.he.net). Compose mounts deploy/secrets/controller read-only at /run/nsc. Keep these files uncommitted with minimal read permissions. NSC_ACME__DNS_PROVIDERS JSON replaces the configuration profile map and must contain secret paths rather than inline credentials; it does not erase database profiles.

A configuration reference takes precedence over a database reference of the same name. Configuration profiles have `source: config`, revision zero, and reject API writes. A shadowed database profile remains stored and becomes active if the configuration entry is removed and the Controller restarts. Prefer distinct names when migrating sources, and configure equivalent profiles on every HA Controller. Missing or invalid configuration files fail startup; unreadable encrypted database profiles fail the affected read or ACME operation, rather than falling back to HTTP-01 or empty credentials.

Strict JSON formats follow; copy the [DNS secret examples](../examples/dns-secrets):

```json
{"access_key":"...","secret_key":"...","security_token":"optional"}
{"api_token":"...","zone_api_token":"optional"}
{"secret_id":"...","secret_key":"...","session_token":"optional"}
{"tokens":{"api.example.com":"DNS.he.net dynamic DNS token"}}
```

- Aliyun also accepts {"ram_role":"role-name"} instead of static access keys.
- Prefer Cloudflare API tokens; {"email":"...","api_key":"..."} remains compatible. One token needs both zone-read and DNS-edit permissions, or supply separate api_token and zone_api_token values.
- For DNS.he.net, precreate a dynamic TXT record at _acme-challenge.<domain> for each exact hostname. The tokens key omits _acme-challenge., for example api.example.com.
- Only exact hostnames are supported, not wildcards. DNS-01 needs neither an online Gateway nor public port 80. HTTP-01 waits for the Gateway challenge revision ACK before CA validation.

Select the profile when creating a managed Domain:

```json
{
  "pool_id": "pool_...",
  "host": "api.example.com",
  "entrypoints": ["http", "https"],
  "redirect_http_to_https": true,
  "tls": {
    "mode": "managed",
    "acme_challenge": "dns-01",
    "dns_provider_ref": "cloudflare-prod"
  }
}
```

Changing tls.acme_challenge or tls.dns_provider_ref creates a new managed certificate and queues the old one for revocation. Certificates persist the choice for renewal. For Discovery Domains, set these fields with an If-Match-protected Domain PATCH.

The Domain form lists redacted profiles from both sources. Managed TLS offers HTTP-01 or DNS-01; DNS-01 also requires a profile. Without profiles, create a database profile as an administrator or mount and configure a file-backed profile and restart the Controller. Domain forms do not receive credential values; an administrator entering a database profile supplies them through the credential form.

To replace a database credential, GET its current metadata/ETag, then PUT the full profile with the new credentials and current password over HTTPS. A stale ETag fails without overwriting the profile. New issuance or renewal resolves the updated profile; an issuance already running may still hold the previous credential, so retain the old provider identity until that work has finished or been safely retried. To replace a configuration credential, update its protected file and restart all Controllers with the same content. Existing references and issued certificates remain unchanged; replacement does not itself request a new certificate or revoke the old provider token. Verify a real challenge with the new credential before revoking the old identity. Keyring rotation and backup retention are described in [Security](security.md#secret-encryption-and-dns-credentials).

## ACME renewal window

controller.certificate.renew_before_expiry defaults to 720 hours. To avoid repeated immediate renewals for short-lived test/private-CA certificates, the effective advance window is capped at one third of total validity, with deterministic Certificate-ID jitter. A 90-day certificate retains approximately 30 days of advance notice.
