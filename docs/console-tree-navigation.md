# Tree navigation and HTTP browser compatibility

Services use `Project/Stack → Service → Container/Task` trees. Routes use
`Project/Stack → Route`. Both are PrimeVue TreeTables with independently paginated
branches. Root groups start collapsed; expanding a group requests 50 children,
and expanding a service requests its first 50 instances. Load more follows the
server cursor. Refresh preserves expansion and submitted filters; background
polling pauses. Remote query state and form drafts remain separate.

Compose projects come from discovery metadata. Swarm stacks come from
`com.docker.stack.namespace`, without guessing by splitting service names. A
missing stack label is an ungrouped service. Different runtimes always produce
different group identities even when project names match. Existing Swarm metadata
is refreshed on the next successful discovery; no database migration is required.
This does not change Service IDs, DNS names, or desired Gateway configuration revisions.

## Read-only API additions

- `GET /api/v1/service-groups?resource=services|routes`: cursor collection containing
  `key`, `name`, `runtime_id`, `runtime_name`, `runtime_type`, and `count`. Counts
  apply the requested filters. Group keys/cursors are opaque to clients.
- Service collections accept `group_key` alongside existing filters. Route
  collections accept Runtime, group, application, management, status, search,
  domain and service filters. Route groups use their backend service's project.
- Route collections with `include=references` include bulk-loaded `service` and
  `domain` summaries for rendering names and links without per-row requests.
- `GET /api/v1/services/{id}/instances?limit=50&cursor=…` returns a cursor collection.
  Omitting both pagination parameters retains the original data envelope. Missing
  services return 404. Invalid group filters and cursors return field-specific
  RFC 9457 errors.

The OpenAPI document and generated frontend types define the complete wire contract.
Authentication, CSRF, ETags and mutation permissions are unchanged.

## Links and browser identifiers

Resource names and references open `/ui/{section}?selected={id}`; the drawer can
be restored by refresh, shared links and browser history. Catalog versions retain
their existing local detail flow because they have no individual detail endpoint.

Domain links open a new window with `noopener noreferrer`. Enabled HTTPS is
preferred, with explicit links for each enabled protocol, using standard public
ports. They do not inherit the Controller's port or local Compose mappings.
Wildcard domains are displayed without an external link.

Identifiers are generated lazily on submission. Browsers without `randomUUID()`
use UUID v4 built from `getRandomValues()`, supporting ordinary HTTP hostnames and
LAN IP addresses as well as localhost and HTTPS. A form's identical retry retains
its idempotency key; changed requests and new actions get new keys. Secrets and
request signatures remain in memory only. If secure randomness is unavailable,
the UI reports a controlled error and disables secret generation; it never uses
`Math.random()` to create secrets or changes global Crypto objects/CSP.

## Regression checks

`npm run test:coverage --prefix frontend` covers identifier compatibility and
lifecycles, domain links, six languages and existing console utilities.

`go test -run 'TestConsole|TestStackNamespace' ./internal/api ./internal/storage`
covers group identity/counts, filters, cursors beyond 200 services, instance
pagination compatibility, and discovery metadata identity. Set
`NSC_TEST_POSTGRES_DSN` and `NSC_TEST_MYSQL_DSN` to isolated databases to exercise
those dialects; otherwise those two subtests explicitly skip. Never point test
DSNs at the running service's database.

`NSC_CONTROLLER_IMAGE=<built-image> ./scripts/e2e-ui.sh` runs the UI tests against
embedded resources in an isolated Controller container. It provides a temporary
HTTPS proxy and a browser-resolved insecure hostname to check real secure-context
behavior. Large service/instance datasets and external domain pages use deterministic
browser fixtures; authentication and Pool/Runtime creation use actual API requests.

## Verified rollout — 2026-09-12

The final standard Controller image was built with version `v0.2.0` and deployed
as `nsc-controller:v0.2.0` (installation-specific image identity omitted). The existing PostgreSQL
and Gateway containers were retained. Startup reported no migrations to run
(schema remains version 7).

Checks completed:

- Frontend lint, TypeScript, OpenAPI drift, production build, and 441 translation
  keys/placeholders across six languages; 19 unit tests passed.
- All Go package tests and `go vet ./...` passed. New grouping/pagination tests
  ran successfully against isolated SQLite, PostgreSQL and MySQL databases.
- All 10 browser scenarios passed against the final embedded image, including
  actual insecure HTTP hostname, localhost and HTTPS origins, secret generation,
  application submission, binding dialogs, 205-service cursor traversal,
  instances, selected-resource filters/history, domain popups and existing UI flows.
- A separate read-only smoke check of the deployed service used an insecure HTTP
  hostname and verified login, all 12 pages, actual Swarm Stack/Service/Task expansion,
  resource deep links, domain links, create-dialog opening and JavaScript/CSP errors.
- The deployed API returned grouped inventory and the Gateway remained READY
  with matching desired/applied revisions. Installation counts and identifiers
  are omitted from this public copy.
- The three existing development domains retained HTTP 308 and HTTPS 200 responses.
  These local routing probes used `curl -k`; they do not validate certificate trust.

Original backup locations, image identities and before/after records are retained
in restricted local evidence. No business resource was created by the historical
smoke check. These 2026-09-12 results are not v0.1.0 acceptance evidence.

A deployed tree screenshot is in `frontend/test-results/deployed-service-tree.png`
(ignored build/test artifact). This update did not modify external service configurations.

## Deleted service history

`GET /api/v1/services` hides `REMOVED` records by default. Set
`include_removed=true` to include history, or request `status=REMOVED` explicitly
(even with `include_removed=false`). Filtering occurs before cursor pagination.
`GET /api/v1/service-groups?resource=services` accepts the same filter; group
counts always describe current services. Internal ownership, recovery and audit
queries continue to retain historical records.

The service workspace provides **Show deleted**, initially off and persisted as
`include_removed=true` in the URL. Deleted rows are labeled separately. Sidebar,
application and group counts exclude them, as do new route backend choices.

Only a successful, fresh, complete discovery observation can establish that an
unmanaged object disappeared or returned. Health sweeps preserve `REMOVED`;
collection failures and disconnected nodes cannot establish deletion. Existing
stopped objects and services scaled to zero remain visible. Missing managed
services remain `UNAVAILABLE` so operators can recover them.

When registered system objects are replaced, reconciliation atomically detaches
confirmed retired, unmanaged services from current application membership.
Historical service records and system protection claims remain intact. This
requires no schema migration and performs no automatic data cleanup.
