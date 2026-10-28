# Application management

NSC provides an opt-in deployment layer above Docker discovery and the DNS-only NGINX control plane. The same Compose document is used for a Docker Compose project or a Swarm Stack. Existing external projects remain read-only until they are explicitly adopted.

## Enablement and backend behavior

Docker mutation is denied unless both `management.enabled` in the Controller configuration and `management_enabled` on the selected Runtime are true. The defaults are false. SQLite supports Compose only; Swarm requires PostgreSQL or MySQL and the Runtime endpoint must connect to a manager.

The executor uses the pinned Docker CLI and Compose plugin without shell command construction:

- Compose deploy/start: `docker compose ... up -d --wait --no-build`; stop/down/restart/scale use the corresponding Compose operations. `down` never passes volume or image deletion flags.
- Swarm deploy/start: `docker stack deploy --prune --with-registry-auth`; convergence is polled. Stop/down removes the running Stack but retains Application state, revision history, named volumes and replica overrides.
- A failed candidate revision automatically redeploys the active revision. A failed first install removes only objects created by that attempt; named volumes and the failure record remain.

Every Docker mutation returns HTTP 202 and a `Location: /api/v1/operations/{id}` header. Managed domain attachment/detachment follows the same rule whenever a network redeploy is required; an external Service that is already DNS-ready only changes control-plane configuration synchronously. Operations are durable and use `QUEUED`, `RUNNING`, `SUCCEEDED`, `FAILED`, `ROLLED_BACK` or `ROLLBACK_FAILED`. Send a unique `Idempotency-Key` on retries. Application configuration changes additionally require the current `If-Match` ETag.

## Install or adopt

Use the Application Market UI or `POST /api/v1/applications`. Supply either `catalog_app_version_id` or inline `compose_yaml`/`env_example`/`manifest`; catalog content cannot be overridden. `project_name` is stable for the Application and becomes the Compose project or Stack name. It must start with a lowercase letter or digit and contain only lowercase letters, digits, dashes, and underscores.

Adoption requires `adopt: true`, an exact service-name match between the submitted Compose and the discovered external project/Stack, and no already-managed service. Adoption attaches immutable configuration history without recreating the running Docker objects. An ordinary install refuses to overwrite any discovered project using the same name.

The deployment override preserves an adopted service's existing pool network connection when discovery confirms its DNS and routing eligibility, including before its routes are imported. Application Compose documents still cannot claim the protected pool network themselves.

Application revisions are immutable. Upgrade flow is: preflight, inspect the redacted Compose/environment plus service/network/volume/deploy differences, then submit the revision with `If-Match`. For an environment-only revision, omit `compose_yaml`; NSC inherits and digest-verifies the latest desired revision (falling back to active), so key-mode edits do not require resubmitting or reconstructing the original Compose document. Catalog defaults and manifest metadata are inherited with the pinned catalog version. Catalog synchronization only exposes newer pinned versions; it never upgrades an Application automatically.

## Environment input

Raw mode is a complete `.env` replacement layered over `docker/.env.example` defaults:

```json
{"environment":{"raw":"DB_NAME=app\nDB_PASSWORD='change me'\n"}}
```

Key mode accepts one entry per key:

```json
{
  "environment": {
    "values": [
      {"key":"DB_NAME","value":"app"},
      {"key":"DB_PASSWORD","value":"change me","secret":true}
    ]
  }
}
```

`raw` and `values` are mutually exclusive. Duplicate/invalid names and missing required variables fail preflight. Parsing follows Docker dotenv quoting, comments and sequential interpolation. Compose operators `${VAR}`, `${VAR:-default}`, `${VAR-default}`, `${VAR:+alternate}`, `${VAR+alternate}`, `${VAR:?error}` and `${VAR?error}` are resolved by NSC before execution; only template defaults and Application values participate. The Controller process environment is never consulted.

In key mode omitted keys remain unchanged, an omitted value retains the previous value, an empty secret retains the stored secret, and `clear: true` explicitly removes a key. Raw mode intentionally replaces the prior user set. Secret values are write-only and appear as `[redacted]` in previews and task logs. `nsc-app.yaml` may generate `password`, `token` or `uuid` values.

## `nsc-app.yaml`

The manifest is optional. Without it, NSC infers the display name/description from README, services and target ports from Compose, and defaults from `.env.example`. A manifest can add validation, grouping, secret metadata, generators and domain suggestions:

```yaml
name: Example
description: Example web application
version: 1.4.0
groups:
  - name: database
    label: Database
variables:
  - key: DB_PASSWORD
    group: database
    required: true
    secret: true
    generator: password
    min_length: 16
    description: Database password
  - key: MODE
    default: production
    pattern: '^(production|staging)$'
    choices: [production, staging]
recommended_domains:
  - service: web
    host: app.example.com
    path: /
    target_port: 8080
    tls_mode: managed
```

Unknown manifest fields fail parsing. `recommended_domains.service` must exist; path, port and TLS mode are validated. Suggestions are returned by preflight but require an explicit domain-binding request.

## Catalog sources

NSC ships a built-in catalog with NGINX, Redis, MySQL, PostgreSQL, WordPress, pgAdmin and phpMyAdmin. See [Application Market](application-market.md) for versions, credentials, networking, persistence and the Compose-only support boundary of the six new templates. An API-created source must be `GIT`, explicitly `trusted`, use an HTTPS URL, and pin a full 40–64 hexadecimal commit/digest. The URL serves a schema-v1 JSON index whose versions embed the exact files:

```json
{
  "schema_version": 1,
  "versions": [{
    "app_key": "example",
    "name": "Example",
    "version": "1.4.0",
    "commit": "0123456789abcdef0123456789abcdef01234567",
    "compose_sha256": "<sha256>",
    "artifact_digest": "<sha256>",
    "files": {
      "docker/docker-compose.yml": "services:\n  web:\n    image: example:1.4.0\n",
      "docker/.env.example": "MODE=production\n",
      "nsc-app.yaml": "name: Example\nversion: 1.4.0\n",
      "README.md": "# Example\n"
    }
  }]
}
```

`docker/docker-compose.yml` is required. `compose_sha256` hashes its LF-normalized bytes. `artifact_digest` hashes, in order, the LF-normalized Compose, `.env.example`, manifest and README, adding one NUL byte after each item. Optional files are treated as empty. The version commit must equal the source pin. All hashes are checked on synchronization and again before install/upgrade.

## Compose safety policy

Services must use prebuilt `image` references. NSC allows explicit environment, healthcheck, named/anonymous volumes, networks, ports/expose, depends_on, command/entrypoint, labels and common deploy policies. It rejects source builds, privileged mode, host namespaces, device access, capability/security/sysctl changes, Docker socket references, volume/network driver options, unsupported drivers and local secret/config files. External secrets/configs must be named in the administrator's `management.external_secrets` / `management.external_configs` allowlists. Private registries use a configured `registry_credentials` reference only.

Administrators may use validated host binds on standalone Compose. Swarm
persistence follows the selected data-node policy. Deployment API keys
cannot use host binds or globally named/external volumes; use application-owned
local volumes instead. Mount source and target must be clean absolute paths below `/`; host
system interfaces and Docker sockets remain forbidden. Long mount syntax accepts
`read_only` and `bind.create_host_path: false`; automatic directory creation and
propagation options are rejected. This permits existing application directories
and file mounts while retaining their original locations and read-only flags.

For the same explicitly restricted Compose service, `container_name` and mapping
syntax `extra_hosts` are supported. Swarm does not support `container_name`.
Import prebuilt images from an existing deployment without its source-build
directive; NSC does not build images. Protected ingress networks remain owned by
the binding system and are emitted through the generated network override.

Adoption validates the stored restriction again before recording ownership and
does not invoke Docker. Deployment commands for node-bound runtimes require the
elected Controller to be on that node and verify the local Docker engine, node
and cluster identity before using its socket. A Controller on another node fails
without deployment, cleanup or rollback commands; cross-node deployment RPC is
not implemented. Compose ignores Swarm placement itself, so the Controller's
node identity check is required even when the YAML contains the restriction.

Preflight reports Compose and Swarm compatibility independently. Compose-only or semantically ignored fields such as top-level `name`, `pull_policy`, `depends_on`, `restart`, `shm_size`, and bridge networks block Stack installation instead of being silently ignored. Device reservations under `deploy.resources` are rejected for both backends.

## Services and domain bindings

### Swarm application networks

New Swarm installations have a shared, application-owned overlay network. Its
actual Docker name defaults to `project_name` (the Stack name), without an
additional Stack prefix. Set `network_name` in application installation or
revision requests to choose another name. Install preflight accepts
`project_name` to resolve the default; environment preflight already knows the
Stack name and accepts `network_name` directly. The console deployment wizard
and application network panel expose the same choice.

Every service joins this network while preserving its source networks and
aliases, including the implicit `default` network. NSC records the selection in
the immutable revision's `configuration_source.network_name`; omitted revision
input inherits the desired revision, and restoration uses the historical
selection. Preflight marks network changes and the console requires review
before creating a revision that updates the services.

NSC creates the overlay with an application ownership label, serializes creation
by network name, and rejects existing networks with another owner or an
incompatible driver. An application cannot use the Gateway Pool network as its
business network. The generated `nsc_application` network key is reserved when
application networking is enabled. Original Compose documents remain unchanged.

Controller upgrades and adoption do not modify running workloads. A subsequent
explicit deployment adds the selected network. Stopping an application retains
it; failed first installations attempt to remove only the network created for
that revision after workload cleanup. In-use or unverifiable networks are
retained, and cleanup failures remain visible. Old networks after a network-name
change are retained; this feature does not implement network garbage collection.

### Console bindings and certificates

The real service detail page provides binding creation, editing and unbinding,
with durable operation links and separate certificate and Gateway evidence.
It uses the Service binding API so managed services receive the generated
Gateway network attachment. Failed bindings can retry application deployment.
Business networks are preserved when the last Gateway binding is removed.

Managed HTTPS offers both `dns-01` and `http-01`. DNS-01 selects a configured
credential profile, never raw credentials. HTTP-01 requires the issuing CA to
reach the domain on port 80; the challenge path remains available even when the
business listener is HTTPS-only. The console defaults new bindings to DNS-01
without changing the API's existing default behavior.

Custom TLS selects an active certificate from the same Pool covering the host.
`POST /api/v1/certificates` accepts a PEM chain and private key without
`domain_ids`, allowing import before a Domain exists. The certificate's DNS SANs
are returned for selection. Supplying existing `domain_ids` retains the existing
atomic import-and-associate behavior. Private keys remain encrypted and
write-only. Custom certificates are replaced manually by importing a new asset
and changing the Domain reference; they are not sent through ACME renewal.

Certificate changes affect every path using that Domain. Existing Domain reuse
does not silently change its TLS settings. Gateway evidence is Pool-level and
does not establish browser reachability; verify DNS and trusted HTTPS separately.

See the [intranet trial guide](intranet-trial.md) for prerequisites and acceptance.

`GET /api/v1/services` supports runtime, application, status, managed and text search filters. Compose uses stable `project/service` keys; Swarm uses the stable Service name, so object-ID changes do not lose bindings. Managed services that are down are represented from the saved revision.

A domain binding joins a Domain and Route to one Service. Compose services use `project_service` DNS aliases and Swarm uses full `stack_service` names. A service needs one unambiguous TCP backend port, or the caller must supply `route.backend.port`. Existing Domains can be reused only in the same Pool with compatible entrypoints and TLS/ACME settings. One host can route different paths to different services.

The v2 binding payload keeps client entrypoints separate from backend transport:

```json
{
  "domain": {
    "host": "app.example.com",
    "entrypoints": ["http", "https"],
    "redirect_http_to_https": true,
    "tls": {"mode": "custom", "certificate_id": "cert_..."}
  },
  "route": {
    "path": "/",
    "backend": {"port": 8080, "protocol": "http", "server_name": ""}
  }
}
```

HTTP-only, HTTPS-only and dual-entrypoint proxying are supported. Redirect is valid only when both entrypoints are present. Backend `protocol` independently chooses HTTP or HTTPS between the Gateway and Service.

On a managed service's first binding, Domain/Certificate/Route, generated network intent and deployment Operation are committed together. The Route remains `PENDING` and is excluded from NGINX snapshots until the redeploy succeeds and its qualified DNS identity is available; failures become `ERROR`. External services are never reconfigured—attach them to the Pool network with the exact qualified alias yourself and retry. There is no IP/VIP fallback.

API bindings can be patched or deleted with ETags. Unbinding disables only that Route and retains Domain/Certificate by default. Label-created bindings are read-only; after an API binding is removed, a matching valid label route may become effective again. Set each Agent's `agent.public_address` (or `NSC_AGENT__PUBLIC_ADDRESS`; the Compose bundle exposes `NSC_GATEWAY_PUBLIC_ADDRESS`) to a public IP or hostname. Bindings then report the required A/AAAA/CNAME target for every enabled Gateway. NSC does not edit public DNS.
