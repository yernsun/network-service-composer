# Archive workspace frontend

The unchanged repository-root `nginx-service-composer.tar.gz` defines the UI and
interaction contract. Its 457 templates, original renderers, styles, action IDs,
route transitions and editor guards are restored. The visible additions are the
project's eight-language selector and theme preferences. Authentication uses
separate `/login` and `/logout` pages outside this workspace. User management and
automatic image-update management pages are deferred.

## Build and run

Use the repository Node toolchain and install with `make frontend-install`.

| Command | Purpose |
| --- | --- |
| `npm run dev --prefix frontend` | Real API workspace, port 5173 |
| `npm run build --prefix frontend` | Assets embedded by the Controller |
| `npm run dev:demo --prefix frontend` | Same Vue client with loopback Faker API, port 5174 |
| `npm run build:demo --prefix frontend` | Build the same Controller frontend assets |
| `npm run preview:demo --prefix frontend` | Serve those assets and Faker API, port 4174 |
| `npm run e2e:demo --prefix frontend` | Shared browser and Faker contract checks |
| `npm run e2e:shared --prefix frontend` | Shared checks against an isolated Controller |
| `make frontend-check` | Lint, types, total branch coverage and build |
| `make harness` | Repository, API/i18n, builds and backend verification |

`VITE_API_PROXY_TARGET` selects the development Controller (default
`http://127.0.0.1:8080`). The default workspace entry is `/projects`.
`api/openapi.yaml` supplies the checked transport types. Locale definitions come
from `internal/i18n/locales.json`; catalogs combine shared backend messages and
`src/shared/i18n/locales`. Production catalogs are compiled without runtime eval.

## Data boundaries

The original controller calls `src/data/api.js` for real reads and writes. Vue
Query caches directory collections used by navigation and maps explicit server
relationships into the original template data. Compose documents, revision
history, variables, pool configuration and instance observations load for the
active page and its project/environment or concrete resource. Navigation waits
for fresh directory and required detail reads after the existing editor guard
admits it, including repeated clicks on the current link. A loading indicator
keeps the current page visible until those reads finish. The workspace and Vue
components share one Query client with separate keys for their return shapes.
Accepted resource writes emit identity-only change notifications, invalidate
related resources and refresh the current page and active component queries.
The following refresh reuses unrelated directory and detail queries, and cancels
older reads of changed resources. Notifications in one batch share a refresh;
a mutation during a read queues another batch and discards the older snapshot.
Navigation cancels previous reads and admits only the latest target. Terminal
deployment outcomes refresh resources even when an operation page is not open.
Background observations follow the visible workload scope. Queued cancellation settles
immediately without waiting for an active network slot. Failed or superseded
reads retain the active snapshot. Background refresh preserves unsaved editor
text, revision baselines, ETags and open modals, including inputs not yet applied
from a modal. Clean Compose and domain editors follow the fresh server snapshot;
dirty Compose editors rebase only when the user explicitly requests it. Local
filter URL synchronization keeps open forms intact. If a write succeeds but its read
fails, a separate notice offers a read-only retry without resubmitting the write.
Typing, local filters and preflight checks do not emit resource changes. Shared
pool membership does not establish project/environment ownership. Writes use
concrete server IDs. Form inputs stay
in memory; deployment wizard drafts are encrypted by the Controller, scoped to
the signed-in user and environment, and saved with ETags.
Sessions and language preference use the existing server contracts; only the
real theme is stored locally. Viewer writes fail through the existing form error.

Vite always selects the real network runtime. The production module graph
rejects the archived simulation controller. `faker/server.mjs` has no Docker
socket, Controller configuration, external proxy, or production credentials;
it binds to loopback and serves the same assets with an in-memory API fixture.
Restarting Faker resets the fixture. Browser storage contains only theme
preferences, never credentials or deployment drafts.
Node was selected so this fixture can reuse the frontend's existing package,
YAML parser, Playwright runner and asset build without a second toolchain.
`shared.e2e.ts` runs against both Faker and an isolated Controller; Faker-only
contract tests cover seeded operations that need deterministic data.

Backend capability gaps retain the original controls and steps. Submission reports
an error through the original form/toast and preserves input. No simulated
success is returned. In particular, domain-local drafts and publish actions cannot
be mapped onto whole-pool NGINX release APIs without changing their scope.
See the tracked [console migration and compatibility notes](../docs/frontend-migration.md).

## Certificate inventory

The all-projects, all-environments certificate view lists every paginated asset
returned by the certificate API, including automatically issued, pending,
renewing and imported certificates without domain links. Narrower project and
environment views retain explicit domain ownership; a shared pool or matching
certificate name does not establish a link. Certificate SANs and linked domains
are displayed separately. Domain search is local and matches primary names,
SANs, linked hostnames and the single-label coverage of wildcard SANs.

An active Vue Query observer reads certificate metadata every five seconds
during issuance or renewal and every thirty seconds otherwise. It refreshes on
window focus and accepted resource changes. Polling pauses while the window is
hidden, and closing the page cancels its reads. Signing updates the visible
status and expiry without navigation.
Refresh preserves the domain filter and retains the last list on read failure;
the retry performs only GET requests. Private certificate material is not
rendered. These observations do not establish certificate distribution or a
loaded Gateway revision.

## Reproducibility and verification

`reference/` records the unchanged archive source and action inventory. To regenerate
the data boundary and explicit translation calls:

```sh
node frontend/scripts/restore-runtime.mjs
node frontend/scripts/localize-archive.mjs
npm run i18n:generate --prefix frontend
npm run ui:check --prefix frontend
```

The integrity check reverses translation keys before comparing original navigation,
editor guards, router integration, compiler behavior, six renderers and templates.
`tests/restoration/parity.e2e.ts` generates fresh expected screenshots from the
untouched archive at port 5175, then compares the restored demo at port 5174 with
identical data, clock, viewport, language and theme. It covers top and sidebar
scope controls, original section fallbacks, dirty cancellation, history and modals.
Historical archive screenshots are not acceptance evidence.

For real execution, build the Controller and Agent, then run:

```sh
NSC_BROWSER_BINARY=/path/to/chrome python3 scripts/e2e-workspace.py
```

The runner creates a labelled, disposable Docker-in-Docker daemon; Controller,
gateways and application workloads remain inside it. It needs local `docker:28-dind`,
`nginx:alpine`, Controller and Gateway images, and registry access for the pinned
NGINX digest. The runner always removes its own container/network and retains
logs, screenshots, traces and reports under `coverage/workspace-e2e/<run-id>`.
The sandbox DNS helper configures only the isolated daemon's namespace.

Frontend admission retains **80% overall branch coverage**, including the restored
business source. There is no frontend 90% incremental gate. Backend requirements
remain unchanged. A successful build or screenshot comparison alone is not complete
acceptance; failed, skipped and unavailable scenarios must be reported separately.

### IP access control

The Domain's NGINX configuration tab includes `access` and `forbidden` lists for
both the Domain and each Route. Use one IPv4 address, IPv6 address, CIDR prefix,
or inclusive `start-end` range per line. The API and version history retain the
original strings, including surrounding whitespace. Empty lines in the editor
are ignored. Generated NGINX configuration uses an exact CIDR cover of each
range; addresses are never enumerated. Scoped IPv6 and IPv4-mapped IPv6 notation
are rejected; use ordinary IPv4 or IPv6 notation.

Forbidden entries take precedence. A nonempty access list denies unmatched
addresses. Domain and Route policies both apply, including fixed responses and
redirects. Empty lists add no restriction. The Domain selects the client IP source
for both its own rules and all Routes: the connection address (default),
`X-Forwarded-For`, `X-Real-IP`, or a custom request header. ACME HTTP-01 challenge
locations remain accessible.

Header mode requires explicit trusted proxy IPs, CIDRs, or inclusive ranges, one
per line. NGINX only replaces the connection address when the connecting peer is
trusted. Recursive resolution scans the header chain from right to left to the
first non-trusted address. Missing or invalid headers fall back to the connection
address; untrusted peers cannot gain access with a forged header. Configure every
trusted hop and list only proxies that control the chosen header. Custom headers
must carry IP addresses, not RFC 7239 `Forwarded` syntax. PROXY protocol and
hostname-based trust are not supported. Header names accept 1–64 ASCII letters,
digits and hyphens, starting with a letter. Trust accepts at most 1,000 entries,
each at most 128 characters. Raw trusted ranges are preserved in drafts/history
and converted to exact CIDRs only during rendering.

The Domain's `server` access control may include `client_ip`:

```json
{"header":"X-Forwarded-For","trusted_proxies":["10.0.0.0/8","2001:db8::10-2001:db8::20"]}
```

Omit `client_ip` to use the connection address. Routes cannot override it. Source
configuration is retained even when both lists are empty, and uses the same
Save draft, Validate, Publish and restore workflow. The Gateway's NGINX must
include `ngx_http_realip_module`; the pinned Gateway image supports it.

Rules are stored in the Pool's versioned `access_controls` alongside snippets,
with `scope: server|location`, `resource_id`, `access`, and `forbidden`. Configuration
allows at most 1,000 targets, 1,000 total entries per target, and 128 characters per
entry. Remove manual `allow`/`deny` directives on a target before enabling its
structured lists. Existing snippets are retained without automatic conversion.

Use Save draft, Validate, and Publish to apply changes. A newer IP policy detected
while saving rejects the stale editor instead of overwriting the restriction.
Preview does not save or activate a configuration. Deploy Controller and Gateways
with the matching `dns-fragments-client-ip-v9` renderer; older Gateways are rejected
by the existing renderer compatibility check. The product version is unchanged.
