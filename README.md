# Network Service Composer for Nginx

Network Service Composer for Nginx (NSC) is a Go control plane for Docker Compose and Swarm. It combines Service/Task/Container discovery, readiness, Domains, Routes and TLS into deterministic NGINX snapshots isolated by Gateway Pool, delivered over mTLS gRPC to an Agent running alongside NGINX.

Community release candidate: **v0.1.0**. See the [release preflight](docs/community-release.md) for acceptance status and support boundaries. The runtime baseline is Go 1.27.3 and NGINX 1.31.5. Controller images include Docker CLI 29.9.2 and Compose 5.6.1. Base images and bundled tools are pinned by digest in [image-lock.json](build/image-lock.json); builds apply current Alpine security updates. Release acceptance binds the resulting image digest and package SBOM.

## Capabilities and boundaries

- Compose and Swarm DNS-only discovery, driven by events and periodic/full reconciliation. Traffic uses qualified Docker Service DNS rather than container, Task or literal VIP addresses.
- Inventory includes stopped Compose services and all Swarm Services. `nsc.enable=true` controls label-created routing, not visibility.
- Optional application installation/adoption from Compose, immutable revisions, preflight differences, durable operations, rollback and replica overrides. External workloads retain their management boundary.
- Built-in and explicitly trusted remote markets, pinned artifacts, SHA256 verification and manually confirmed upgrades. A [static manifest](docs/marketplace-manifest.md) can describe Compose, env parameters, README, manuals, file endpoint and website links.
- Raw/key-value environment editing, write-only secrets, secure generation and AES-256-GCM encryption of managed definitions.
- Service domain/path/TLS bindings, generated Pool network overrides for managed applications and diagnostics for externally managed services.
- Separate Runtime, instance, DNS probe and freshness states. Probe outcomes affect status and issues, not upstream peer lists.
- Pool network boundaries, separate config/probe revisions, probe leadership and durable epochs.
- Multiple domains/paths, shared DNS upstreams, path stripping, WebSocket, separate HTTP/HTTPS client and backend transports, and optional per-domain HTTP/3.
- Multiple PostgreSQL/MySQL Controllers with one elected worker leader, authenticated forwarding and one-second revision polling; PostgreSQL adds LISTEN/NOTIFY. SQLite is limited to one Controller and Compose.
- Bearer REST API, ETag/If-Match, RFC 9457 errors, cursor pagination and an embedded Vue 3 + PrimeVue console.
- ACME HTTP-01 and DNS-01 (Aliyun, Cloudflare, Tencent DNSPod and DNS.he.net), custom certificates, encrypted private keys, key rotation and offline CA/Agent identity issuance.
- Candidate `nginx -t`, atomic generation switching, graceful reload and Last Known Good recovery; the latest three generations remain on disk.

These capabilities do not establish that every Community acceptance gate is complete. See the [Community development record](docs/community-development.md) and [coverage limitations](docs/test-coverage.md).

## Install NSC on Swarm

On Linux with Docker Engine already installed, run the Controller CLI on the
selected maintenance node:

```sh
nsc-controller bootstrap init --interactive --state-dir /var/lib/nsc-bootstrap
```

Supply trusted SSH host identities and verified image digests. The CLI shows the
resource plan before confirmation, prepares Swarm, creates the Gateway project
and registers the four system applications. The initial console uses node-IP
HTTPS on port 8443 with a separate self-signed certificate, valid for 20 years by
default. No domain is required.

The three-Manager topology provides Controller/Gateway redundancy with one pinned
PostgreSQL database and one Sandbox. PostgreSQL remains a single point of failure.
Use the [bootstrap and recovery guide](docs/bootstrap.md) for configuration-file
installation, existing-installation adoption and administrator-only system
maintenance at `/infrastructure/installation`.

## Start from source with PostgreSQL

Requirements: Go 1.27.x, Node.js 24.20.0/npm for frontend builds, Docker Engine, Compose v2, OpenSSL and access to the Go module registry. This is a source-build path, not proof of a build-free end-user installation.

```sh
make build
./scripts/bootstrap-secrets.sh
docker network create nsc-ingress
docker compose up -d postgres controller
```

Open `http://localhost:8080`. Initially sign in with `NSC_ADMIN_USERNAME` and `NSC_ADMIN_TOKEN` from `.env` (the token is the initial UI password). Create a Gateway Pool with backend network `nsc-ingress` and challenge upstream `http://controller:8080`, then add a Runtime:

```text
type: docker
endpoint: unix:///var/run/docker.sock
```

Issue an Agent identity and start the Gateway:

```sh
GATEWAY_ID=$(./bin/nsc-controller id gateway)
./scripts/issue-agent.sh pool_... "$GATEWAY_ID"
# Put the actual Pool and Gateway IDs into .env.
docker compose --profile gateway up -d gateway
docker compose -f examples/compose-app.yaml up -d
```

Application mutation defaults off: both global `management.enabled` and Runtime `management_enabled` must be enabled, followed by explicit installation or adoption. External Services can be discovered and bound when their networking is already suitable; NSC does not modify their networks or lifecycle.

## Database profiles

- PostgreSQL: `docker compose up -d postgres controller`.
- MySQL: `docker compose --profile mysql up -d mysql controller-mysql`; create the Pool with challenge upstream `http://controller-mysql:8080`, issue its Agent identity, then start `gateway-mysql`.
- SQLite: `docker compose --profile sqlite up -d controller-sqlite`; create the Pool with challenge upstream `http://controller-sqlite:8080` and issue its Agent identity as above. Set the actual `NSC_POOL_ID`, `NSC_GATEWAY_ID` and `NSC_SQLITE_LOCAL_GATEWAY=true` in the private `.env`, then recreate `controller-sqlite`. Its embedded Gateway shares the Controller's process-owned SQLite leader store and persists NGINX state beneath `sqlite-data/gateway-state`.

Explicit service names avoid starting the default PostgreSQL services unintentionally. SQLite rejects HA configuration and databases containing Swarm Runtimes.

Standalone production and sandbox Gateways require access to the same PostgreSQL/MySQL store as their Controllers for authenticated leader discovery. The Compose profiles supply matching private DSNs and advertise the correct Controller service addresses; production, sandbox and MySQL Gateways use separate persistent state volumes. The SQLite profile runs one Controller with an optional embedded Gateway; no standalone `gateway-sqlite` service is supported. Stopping that Controller also stops its embedded NGINX data plane, so use shared-store Controllers with independent Gateways when control-plane outage serving is required. Each topology needs its own Pool/Agent identity and protected state; do not reuse an existing Gateway state volume with another Pool.

## API and languages

The REST API is `/api/v1`; see [OpenAPI](api/openapi.yaml). Except health and ACME challenge endpoints, requests require authentication:

```http
Authorization: Bearer <NSC_ADMIN_TOKEN>
```

PATCH/DELETE also require the ETag returned by GET:

```http
If-Match: "rev-3"
```

Local roles are administrator and global viewer; see [security](docs/security.md). Browsers use HttpOnly session cookies and `X-CSRF-Token` for writes; external clients can use Bearer authentication.

Environment-scoped automation can use the separate [Deployment OpenAPI](api/openapi-deployments.yaml) at `/openapi/v1` with an independently issued API key. See the [deployment API guide](docs/openapi-deployments.md) for issuance, preflight, idempotent submission and operation polling.

API, console and official market documentation support **en, ja, de, zh-CN, zh-TW, fr, ru and es**, defined in [locales.json](internal/i18n/locales.json). Automation must use stable error `code`, `params` and `errors[].pointer`, never localized `title/detail`. See [configuration](docs/configuration.md) for negotiation and label DSL.

## Documentation and development

- [Application management and domain bindings](docs/application-management.md)
- [Built-in application market](docs/application-market.md)
- [Static market manifest and multilingual manuals](docs/marketplace-manifest.md)
- [Operations, backup, recovery, upgrades and HA](docs/operations.md)
- [Project environments, image updates and NGINX editing](docs/environments-nginx.md)
- [Cluster console](docs/cluster-console.md) and [HTTP/3](docs/http3-overview.md)
- [Development and test matrix](docs/development.md), [frontend](frontend/README.md), [architecture](ARCHITECTURE.md) and [Gateway protocol](api/proto/nsc/gateway/v2/gateway.proto)
- [Agent instructions](AGENTS.md), [rules](.agents/rules) and [project skills](.agents/skills)

```sh
make frontend-install
make harness-check  # Contracts, documentation, static checks and required assets
make harness        # Also builds binaries and runs unit/coverage gates
```

The tracked installation acceptance exercises PostgreSQL/MySQL standalone Gateways and the SQLite embedded Gateway using an owned nested Docker daemon. Supply current full production images (the Controller needs its Docker CLI and Compose binaries):

```sh
python3 scripts/e2e-installation.py --controller-image nsc-controller:v0.1.0 --gateway-image nsc-gateway:v0.1.0 --output coverage/installation
```

The output directory must be new. Keep copied configuration, PKI and diagnostics private; only `summary.json` is suitable for CI artifacts. The test verifies fresh loaded revision/digests, real HTTP routing, persisted restart and the documented Controller-outage behavior; it does not initialize or join the host's Swarm.

Coverage follows the [risk-based testing policy](.agents/rules/testing.md) and its [single threshold configuration](.agents/rules/coverage.json). Critical business scenarios require behavior assertions regardless of the aggregate percentage. Test failures and missing or stale evidence still block acceptance. Reports and measurement limitations are documented in [test coverage](docs/test-coverage.md).

Out of scope: source builds, host bind mounts/privileged namespaces/devices/Docker sockets in managed applications, automatic public DNS, volume/image cleanup, TCP/UDP passthrough, WAF, complex policies, organizational RBAC, Kubernetes, wildcard DNS-01 domains and additional DNS providers. Keep installation-specific deployment evidence in ignored local reports; see [repository privacy](docs/repository-privacy.md).

See [the repository rename handoff](docs/repository-rename.md) for the new repository address and runtime identifier changes.
