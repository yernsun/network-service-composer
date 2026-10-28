# Project environments, image updates and NGINX configuration

See the [practical operations ledger](practical-readiness.md) for implemented
capabilities, remaining P0 work and deployment/acceptance boundaries.

Console entrypoints are `/projects` and each environment overview at `/projects/{project}/environments/{environment}/overview`. Associating an adopted Application with a logical project does not redeploy it or change Docker projects/Stacks, volumes or domains. Externally discovered applications require adoption first.

## Environments and variables

Each environment maps to one Application and may select a different Runtime. Deployment names combine project name, environment name and a random short ID. Copying an environment copies Compose, not business data or variable secrets. Different deployment names do not isolate explicitly named resources.

Merge order is Compose/template defaults, project variables, then environment overrides. Empty strings are valid; masking an inherited key differs from deleting an override. Overridden shared secrets remain secret. Key/value and .env input are mutually exclusive. Importing .env replaces the current layer and removes omitted keys. Secrets are encrypted and not returned on read; a new value replaces them while omitted fields retain existing values.

Saving variables only changes pending configuration. Preflight creates a 15-minute encrypted snapshot and image locks; confirmation submits preflight_id. Variable/environment revision changes invalidate it with 412. The snapshot, source revisions and all service image digests persist with ApplicationRevision. Manual retries reuse the same idempotency key.

Preflight lists explicit network/volume names, external resources, published ports and same-name resources in deployed Applications on the Runtime. Ordinary named volumes use deployment-name isolation. Host bind mounts require a provisioned node Runtime and matching exact `node.id` placement restriction; see [Application management](application-management.md). Domain binding/conflict checks remain mandatory. The Runtime also checks port availability.

## Updating fixed tags

Detection defaults off. Select services and webhook/polling when enabling it. All new environment policies default to manual confirmation, including dev/test. Existing saved automatic-update choices are preserved. Polling defaults to 300 seconds, minimum 60, with jitter, timeouts, bounded concurrency and failure backoff including Registry 429 Retry-After.

First deploy a successful digest baseline for every service. Missing historical image identity cannot be replaced with the tag's current content. Digest references are not tag-tracked. Registry checks read manifests/indexes, not layers; Docker downloads layers during deployment. Private registries use configured registry_credentials and HTTPS. Only isolated test registries may explicitly use insecure: true.

Each check freezes all images into one environment candidate. Automatic updates use the last successful Compose/variable snapshot, not pending edits. Stopped applications, intentional zero replicas, disabled management or queued/running operations prevent competing deployments. Candidate confirmation binds the base deployment revision; changes require a fresh check.

Failed deployments use existing rollback and do not automatically repeat failed candidates. Administrators may retry or await a new candidate. Database coordination supports multiple Controllers; durable idempotency keys recover interrupted submission associations. Interrupted submissions that were not enqueued become failed and require explicit retry.

**Rollback restores configuration and images, not data written to databases or volumes.**

## CI Webhook

Enable the webhook on the environment update page and generate its independent secret. The secret is shown once; rotation immediately invalidates the old one. Interface:

```
POST /api/v1/hooks/image-updates/{hookId}
X-NSC-Timestamp: <Unix seconds>
X-NSC-Event-ID: <unique event ID>
X-NSC-Signature: <hex HMAC-SHA256>
Content-Type: application/json

{"services":["web"]}
```

Sign the raw bytes timestamp + "." + eventID + "." + body using the returned secret string without hex-decoding it. The time window is five minutes; persisted event IDs prevent replay. Cookie/Bearer authentication cannot replace the signature. Payloads cannot supply Compose, environment variables, arbitrary URLs or commands. Events trigger a fresh query of the configured registry rather than trusting a supplied digest.

Run the supplied script after a successful image push:

```sh
export NSC_WEBHOOK_URL='https://controller.example/api/v1/hooks/image-updates/HOOK_ID'
# Inject NSC_WEBHOOK_SECRET from CI secrets; keep it out of history and logs.
export NSC_IMAGE_SERVICES='web worker'
python3 scripts/image-update-webhook.py
```

GitHub Actions example:

```yaml
- name: Notify environment after pushing images
  env:
    NSC_WEBHOOK_URL: ${{ vars.NSC_WEBHOOK_URL }}
    NSC_WEBHOOK_SECRET: ${{ secrets.NSC_WEBHOOK_SECRET }}
    NSC_IMAGE_SERVICES: web
  run: python3 scripts/image-update-webhook.py
```

## NGINX drafts, versions and publication

Edit Pool http, Domain server and Route location snippets. Each is limited to 64 KiB and supported body-size, proxy timeout/buffering, response-header, gzip and IP-access directives. External includes, nested blocks and reserved variables/headers are prohibited. NSC retains routing, certificates, WebSocket, ACME and health configuration. Specific snippets may override matching parent headers while retaining other inherited headers and X-Request-ID.

Context examples explain http/server/location syntax and units. NSC connect/read/send timeout defaults are 5s/60s/60s. Other placeholders are examples and are never saved or applied automatically. Insert-example works only on empty inputs.

Preview uses unsaved snippets without changing drafts or Pool revisions. Domain/Route edits select their owning domain; shared HTTP snippets allow domain selection. The default complete.conf expands the main configuration, shared directives, related upstreams, server/location and system configuration; original files remain available. Certificate paths remain, private keys are omitted, and copied configuration still needs its runtime certificate files.

Colors, source labels and change markers identify custom, edited and inherited regions and changes from published snippets using the same business snapshot. Deleted lines display separately; copying returns candidate configuration only. Source labels navigate to input lines. Input edits mark previews stale; syntax errors identify the input and line. Preview is not Gateway/sandbox verification.

POST /api/v1/gateway-pools/{id}/nginx/preview accepts snippets, optional domain_id and focus_index. Explicit domain selection wins; otherwise the backend resolves the focused snippet's domain without relying on frontend pagination. Responses contain candidate/active configuration, original files, one-based source ranges, domains, business revision and renderer version. GET preview remains compatible.

Saving drafts never publishes. Preview final .conf files, compare history, validate and then publish. Restoring snippets creates a new draft against current resources without decreasing Pool revision. Each Pool retains the latest 100 published versions plus current/previous active versions by default; runtime snapshot GC remains unchanged.

Confirmation freezes current production Gateways and blocks if any enabled target is offline. Publication stores the encrypted complete candidate and proceeds sequentially. Failure or unknown results stop unstarted targets. Per-target prior version, result and verification time remain visible; newly joined Gateways do not join old tasks. ACK validation requires matching version/digest, not merely a newer revision.

Stop affects unstarted targets only. Verification requires valid runtime evidence newer than task start; unconfirmed results remain unknown. This flow currently covers extension snippets only, not automatic Route/certificate/discovery publication. Arbitrary target subsets and complete retry, accept-current-state or group-recovery flows remain incomplete.

Validation binds the draft, business configuration, sandbox settings and renderer for five minutes. New validation, reconnect, cancellation, expiry or configuration changes invalidate prior results.

## Dedicated sandbox

agent.mode / NSC_AGENT__MODE accepts production (default) or sandbox. Create a separate Gateway ID and mTLS identity, bind it in the Pool NGINX page, then start it. Registered production Gateways cannot convert to sandbox.

The local Compose profile uses 127.0.0.1:18080/18443, separate state storage and deploy/secrets/sandbox. Generate an ID with nsc-controller id gateway and issue its certificate with the existing CA's pki issue-agent. Store ca.crt, agent.crt and agent.key with standard Gateway permissions.

```sh
export NSC_SANDBOX_GATEWAY_ID=gw_...
export NSC_SANDBOX_POOL_ID=pool_...
# Optional: NSC_SANDBOX_BIND / NSC_SANDBOX_HTTP_PORT / NSC_SANDBOX_HTTPS_PORT
# Bind the dedicated identity in the console before starting:
docker compose --profile sandbox up -d gateway-sandbox
```

Independent nodes use the same Agent configuration and explicitly configured network connections. NSC neither scans nor opens Docker APIs. Sandboxes are excluded from production inventory, probe elections, business publication and ACME issuance.

Validation first uses isolated in-sandbox backends for HTTP/HTTPS, WebSocket, gzip, body size, proxy timeout, invalid-config rejection and recovery. Optional real-route GET/HEAD requests target the sandbox loopback entry, preserving Host/SNI without redirects. Successful candidates remain until expiry for mapped-port checks; timeout, cancellation or disconnect restores baseline. scripts/verify-sandbox-entry.sh accepts the host, address and ports described in its usage.

Sandbox validates NGINX configuration only. Image updates still use Application preflight, deployment and readiness checks; dev/test workload isolation comes from separate environments.

## Build and verify

Production images run Go/NGINX only; frontend assets are built and embedded. Migration 00008 adds control documents and deployment-source records to all three databases, increasing existing Pool revisions to distribute new rendering. Upgrade Controller and Gateway to matching renderers.

```sh
make frontend-check
make test
make test-e2e-delivery  # Temporary registry, Compose and isolated Docker-in-Docker Swarm
make test-e2e-nginx     # Existing Compose regression and complete sandbox flow
make test-e2e-ui        # Browser regression against embedded assets
```

Existing keys reencrypt covers variable layers, history, environment copies, preflight snapshots and webhook secrets without changing configuration revisions. Back up database/keys before upgrade. Never automatically adopt external Stacks or enable their update policy.

For direct curl calls, construct the signature as follows (CI injects NSC_WEBHOOK_URL and NSC_WEBHOOK_SECRET):

```sh
export timestamp="$(date +%s)"
export event_id="$(python3 -c 'import uuid; print(uuid.uuid4())')"
export payload='{"services":["web"]}'
signature="$(python3 -c 'import os,hmac,hashlib; e=os.environ; b=(e["timestamp"]+"."+e["event_id"]+"."+e["payload"]).encode(); print(hmac.new(e["NSC_WEBHOOK_SECRET"].encode(),b,hashlib.sha256).hexdigest())')"
curl --fail-with-body --silent --show-error \
  -H 'Content-Type: application/json' \
  -H "X-NSC-Timestamp: $timestamp" \
  -H "X-NSC-Event-ID: $event_id" \
  -H "X-NSC-Signature: $signature" \
  --data-binary "$payload" "$NSC_WEBHOOK_URL"
```

## Outbound notifications

Administrators can configure a shared signed Webhook under **Account → Notification
 delivery**. Notifications default off and cover all projects. Select image-update
availability/submission failure, deployment success/failure/rollback results,
certificate problems/recovery, and Gateway offline/recovery independently. A
certificate within 30 days of expiry also requires attention. Only confirmed
READY Gateway observations with a fresh, non-future heartbeat clear an offline alert; disabled or unknown targets do
not imply recovery. Existing historical image/deployment results are not replayed
when notifications are enabled; current certificate/Gateway problems are reported.

Save a public HTTPS receiver URL, a 16–256 byte signing secret and the current
administrator password. URL paths/query parameters and secrets are encrypted,
never returned on reads, and participate in key rotation and recovery checks.
Leave credential inputs blank to retain them. Private, loopback and link-local
receivers are unsupported; resolved addresses are checked at connection time,
redirects are rejected, and receiver response bodies are discarded.

The Webhook JSON contains a stable `id`, `type`, `resource_id`, `occurred_at`, and
optional `environment_id`, `operation_id`, and `status`. It excludes Compose,
environment values, credentials and raw failure diagnostics. Headers are
`X-NSC-Event-ID`, `X-NSC-Timestamp` (Unix seconds) and `X-NSC-Signature` (hex
HMAC-SHA256 over `timestamp + "." + eventID + "." + rawBody`). Receivers should
verify the signature/time window and deduplicate by event ID.

The leader scans durable source records every five seconds. An outbox persists
attempts and observations across restart; concurrent workers use revision checks
and a one-minute delivery lease. Network errors, HTTP 408/429 and 5xx retry with
backoff up to five attempts; Retry-After is bounded to 24 hours. Other non-2xx
responses fail without automatic retry. A timeout can occur after receiver
acceptance, so delivery is at least once, not exactly once. A 2xx response proves
receiver acceptance, not delivery to a human. Gateway/certificate scans observe
persisted current states; transitions entirely between scans may not be reported.

Settings changes cancel pending deliveries from the previous settings revision;
a request already in flight may finish. Administrators may explicitly retry a
failed/cancelled delivery against current saved settings. Test queues a synthetic
event; inspect its delivery record for the actual result. Neither notification
failure nor disabling notifications changes application deployment results.
Recovery inspection never starts the notification worker. Records are retained
until operator-managed database retention; automatic pruning is not implemented.

API: `GET/PUT /api/v1/notification-settings`, `GET /api/v1/notifications`,
`POST /api/v1/notifications/test`, and `POST /api/v1/notifications/{id}/retry`.
All endpoints are administrator-only. Writes require `If-Match` and
`X-NSC-Reauthenticate`; reads use `Cache-Control: no-store`.

## Self-signed certificates

Administrators can create a self-signed certificate from the certificate inventory
or the custom TLS section of a domain draft. Choose a Gateway pool and one or
more exact DNS names. The default validity is **20 calendar years** from issuance;
`validity_years` may explicitly select 1–100 years. DNS names are normalized and
deduplicated. Wildcards and IP addresses are not accepted by this issuance path.

`POST /api/v1/certificates/self-signed` accepts `pool_id`, `domains`, optional
`validity_years`, and optional existing `domain_ids` to attach atomically within
the same pool. Without `domain_ids`, issuance creates an independent asset and
does not change domain bindings. The console creates independent assets, selects
them in domain drafts, and applies them through the normal save/publication flow.

Certificates use ECDSA P-256, DNS SANs and server-authentication usage, with a
five-minute not-before allowance for clock skew. They are self-signed leaf
certificates, not a local certificate authority. Clients must explicitly trust
the certificate; issuance does not establish public browser trust or domain
ownership. There is no ACME request, DNS challenge or automatic renewal.

The `self-signed` provider is shown separately from imported and ACME assets.
Bindings use custom TLS. Private keys use the existing encrypted certificate
storage and are decrypted only for Gateway distribution; ordinary API responses
never return PEM or private-key material. Expiry and revocation continue to block
invalid bindings. Create and select a replacement certificate before expiration.

## Environment capability prerequisites

The environment overview reads `GET /api/v1/environments/{id}/capabilities`
without caching. This is a read-only configuration and image-baseline summary,
not a live Docker, Manager, Gateway, certificate or end-client health check.
`CONFIGURED` means Controller/runtime management configuration permits later
checks; `BASELINE_READY` means the managed application has a verified,
digest-pinned active revision with tag references that can be tracked. Neither
state authorizes deployment or proves availability. Existing execution-time
checks remain authoritative.

The summary explains protected system resources, disabled management/runtimes,
unsupported SQLite/Swarm management, missing applications, adoption requirements,
missing image baselines and fixed-digest-only revisions. Failed reads display
unknown rather than retaining a current-looking successful result. The UI
refreshes after mutations and keeps requests scoped to the selected environment.
Runtime endpoints, credentials and resolved Compose contents are not returned.

### Selected Runtime Manager capability

Cluster overview exposes `manager_available` for the selected Runtime endpoint.
It requires enabled management, a fresh successful Manager observation and a
nonempty cluster identity. Node-collector connectivity does not grant or revoke
this endpoint capability. A different fresh Manager can supply inventory without
making a stale or Worker endpoint deployable. Missing, future-dated or failed
observations do not grant capability. This field describes technical capability,
not user authorization; execution checks remain mandatory.

The console clears old Manager capability when a background inventory request
fails. The environment overview wraps its title, breadcrumbs and action buttons
for narrow displays and long names.

### Image update console

The environment overview includes image detection settings, tracked tag
references and digest baselines, last-check evidence, paginated candidates and
links to deployment operations. Polling and signed CI webhooks are configured
independently. New policies start with detection and automatic deployment off.
Webhook keys are generated or rotated explicitly and displayed once; rotation
invalidates the previous signing key. Clear the displayed secret after storing
it in the CI secret manager.

Saving a policy does not deploy an application. Enabling detection requires
a managed application with a successful digest-pinned baseline and tracked
tag references. Use the application deployment review to establish that
baseline deliberately; the console never silently redeploys an adopted app.
Checks use saved settings and deployments use the successful application
snapshot, not unsaved editor input.

Candidate deployment, retry and ignore actions require explicit confirmation
and the candidate revision. A changed receipt invalidates an open confirmation;
a changed baseline blocks deployment and retry. A stale or failed candidate read
disables mutation and labels retained rows as historical. Authorization failure
hides cached settings and candidates. Viewers cannot mutate policies or keys.
Background refresh preserves unsaved settings; external policy edits require
explicit reload before saving. An accepted request is not deployment success.

### Runtime availability notifications

The optional `runtimes` event group reports enabled Runtime management/discovery
unavailability, degraded state, missing observations and expired evidence as
`runtime.problem`. Recovery requires `ONLINE`, no recorded error and a present,
nonfuture, fresh observation. Disabled Runtimes do not produce recovery claims.
Initializing Runtimes without an error receive an observation grace period.

The expiry/grace budget is the greater of three full-discovery intervals and
the configured stale-endpoint timeout, allowing quiet engines without events.
Explicit offline/degraded/error evidence does not wait for this budget. This is
not an independent application or control-plane liveness probe: a complete NSC
outage still requires external monitoring. Repeated unchanged problems are
deduplicated; endpoints and raw runtime diagnostics are omitted from payloads.
