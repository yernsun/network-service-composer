# Development and release

## Repository privacy

Keep operator deployment evidence and environment files outside Git. Run
`make privacy-check` and install local commit/push checks with
`make install-git-hooks`. The hooks check staged bytes and outgoing commit trees;
CI also checks its committed snapshot. See [repository privacy](repository-privacy.md)
for examples, local report storage and the separate history-cleanup boundary.

## CI isolation and recovery fixtures

The Ubuntu 24.04 unit job grants user namespace access to `/usr/bin/unshare`
through a dedicated AppArmor profile and verifies that the resulting network
namespace contains only loopback. It does not disable the system-wide AppArmor
restriction or bypass namespace tests. The profile is unloaded after the checks.
Disposable unit-test data uses a private tmpfs directory to avoid disk fsync
contention during SQLite coverage and race suites. Both `TMPDIR` and `GOTMPDIR`
point there because Go 1.27 test directories honor `GOTMPDIR`. Build caches and
performance benchmarks remain on disk.
This is not evidence of disk durability or production storage performance.
CI allows 60 minutes per race-test package and 120 minutes for the combined unit
job, including frontend checks, compilation, fuzzing, benchmarks and vulnerability
scanning. These are cumulative execution budgets for hosted runners; individual
production operation deadlines and coverage thresholds remain unchanged.
Retention tests cancel at an observed state boundary rather than assuming a SQL
write will complete within 20 milliseconds under race instrumentation.

Recovery and reconciliation helpers are built with `CGO_ENABLED=0` because they
run inside the Alpine controller image, independently of the build host's libc.
Both fixtures retain their real database, protected-write and recovery checks.
The Swarm retention fixture waits for PostgreSQL TCP readiness before starting
controllers. Socket-only readiness can succeed against the image's temporary
initialization server; exhausting the readiness deadline must fail the fixture.
After readiness, the fixture runs a single migration before starting either HA
controller, with automatic migration disabled in both controller configurations.
Concurrent schema initialization is not part of the supported HA install sequence.
ACME acceptance also waits for the actual TLS endpoint to serve the newly issued
certificate fingerprint, with CA and hostname verification enabled. An ACTIVE
certificate or a previous configuration ACK alone does not prove distribution.
The same check applies to renewal and recovery, so serving an old certificate
cannot satisfy the new generation's acceptance.

## Temporarily disabled verification

At the maintainer's request, the OVH-specific provider-construction and profile
validation tests are marked `DISABLED` and explicitly skipped. The provider
dependency resolves the OS account home rather than the test's temporary HOME,
which makes its configuration lookup fail in the isolated user namespace.
Their original assertions are retained for re-enabling after the fixture is
corrected. OVH runtime support is unchanged; passing gates do not constitute
current OVH acceptance evidence.

## Toolchain

- Go 1.27.3
- protoc 33.0 / protobuf compiler reports v6.33.0
- protoc-gen-go 1.36.12
- protoc-gen-go-grpc 1.6.0
- NGINX OSS 1.31.5 for real configuration tests and the gateway image

```sh
make generate
make verify-generated
make test
make test-race
make test-fuzz
make test-e2e-compose
make test-e2e-management
make test-e2e-ui
make test-e2e-acme
make test-e2e-ha
# Requires an active local Swarm manager:
make test-e2e-swarm
make bench-large
make lint
make vuln
make build
```

Branch and native coverage tests use explicit 15-minute per-package deadlines.
The full harness runs preparation, branch measurement, native statement
measurement and final admission as separate steps, each bounded by 20 minutes.
`make test-coverage-backend` still runs the complete gate in sequence and retains
independent measurements after a branch failure.
Race checks use fresh results and an explicit 30-minute per-package deadline.
The Core package includes many isolated SQL fixtures whose instrumented runtime
can exceed Go's default 10-minute deadline. CI reserves 60 minutes for the unit
job's sequential harness, race and remaining checks. Coverage thresholds and
the harness's own step deadlines are unchanged.

`make generate` only rewrites checked-in protobuf output. `make verify-generated` generates into a temporary worktree copy and fails if output differs.

Build the local linux/amd64 verification image with `make docker-build-verification-toolchain`, or run `docker build -f build/Dockerfile.verification -t nsc-verification-toolchain:go1.27.3-protoc33 .` when `make` is unavailable on the host. The image pins Go 1.27.3, Node 24.20.0, protoc 33.0, protoc-gen-go 1.36.12 and protoc-gen-go-grpc 1.6.0; its build fails if any version differs. Run protobuf verification without network access using:

```sh
docker run --rm --network none --user "$(id -u):$(id -g)" \
  -v "$PWD:/work" -w /work \
  nsc-verification-toolchain:go1.27.3-protoc33 make verify-generated
```

The full harness still requires its documented Go module cache and Linux namespace support. The verification image supplies tools; it does not waive or bypass a failed coverage or isolation gate.

Repository SQL uses descriptive `:parameter_name` markers. `storage.BindSQL`
compiles each occurrence to the placeholder syntax required by PostgreSQL,
MySQL or SQLite before execution. Arguments remain in SQL occurrence order;
repeated marker names consume separate arguments. This keeps one query text
across all three drivers, including MySQL, whose Go driver does not accept
`database/sql` named arguments.

## i18n catalogs and API errors

The embedded catalogs live under `internal/i18n/catalogs`. English is the only fallback and every locale file must have the exact same keys and named `{placeholder}` set. Package initialization fails on malformed JSON, a missing or extra key, an empty translation, or placeholder drift. Add a stable structured error code in the application layer before adding its `problem.<code>` translations; API handlers must never translate by comparing `err.Error()` text.

The Vue console merges these catalogs with frontend-specific messages at build time. The browser translates Problem responses from `code + params` and uses localized `detail` only for an unknown code. The locale authority is `internal/i18n/locales.json`, currently covering eight languages. When changing a catalog or error contract, run `go test ./internal/i18n ./internal/api` in addition to the full suite.

`make bench-large` builds a deterministic synthetic data set containing 10,000 Services, 50,000 Endpoints and 20,000 Domains/Routes. It measures a steady-state discovery signal through the transactional Store, then reports Snapshot encoding time, raw/compressed size and allocations. Snapshot generation, validation and reload latency are also exported at runtime so production P95 can be evaluated over the complete state-signal path.

## Test matrix

- Unit/fuzz: state transitions, label/host/path normalization, keyring, NGINX escaping, deterministic snapshot codec, PKI identity, dotenv/Compose interpolation, safety policy, Catalog digest validation, immutable revisions and deployment rollback.
- SQLite contract: runs in ordinary `go test ./...`.
- PostgreSQL/MySQL contract: set `NSC_TEST_POSTGRES_DSN` and/or `NSC_TEST_MYSQL_DSN` and run the external conformance test.
- Compose E2E: `scripts/e2e-compose.sh` creates an isolated network, starts the real controller/gateway images and scales a labeled application to three containers. It verifies Docker Health degradation/recovery, a Controller outage, a Gateway cold-start from its persisted LKG, reconnect/ACK convergence and live NGINX traffic.
- Management E2E: `scripts/e2e-management.sh` installs a Compose application through the API with raw and key/value environments, exercises restart/scale/stop/start/down, binds a path/domain through the generated network override, and verifies failed-upgrade rollback. It requires the same real Controller image and Docker socket as the Compose suite.
- UI E2E: `scripts/e2e-ui.sh` uses headless Chrome/Chromium against a real SQLite Controller. Its browser scenarios verify localized login/admin surfaces (historical six-language results do not establish coverage of all eight current locales), persistent locale selection without session loss, Problem-to-field translation, Gateway Pools rendering and the embedded Vue assets without adding a production Node runtime.
- Swarm E2E: `scripts/e2e-swarm.sh` requires a local manager and creates only uniquely named temporary resources. It verifies Direct/VIP, three replicas, uninterrupted rolling update, task failure/replacement, endpoint drain and missing overlay diagnostics.
- NSC rolling image acceptance: `make test-swarm-rollout-unit` tests the operator script; `make test-swarm-rollout NSC_CONTROLLER_IMAGE=<image> NSC_GATEWAY_IMAGE=<image>` uses three nested daemons and real NSC HTTP/TLS traffic. See [NSC Swarm upgrades](nsc-swarm-upgrade.md).
- Gateway rolling update: `make test-gateway-swarm-rolling` creates a nested single-node Swarm. It checks global ingress publication on HTTP 8200, start-first update and rollback policy, 30-second stop grace, the replacement Task running before the old Task shuts down, and the final v2 response. See [Gateway Swarm rollout](gateway-swarm-rollout.md) for the separate live-service procedure.
- ACME E2E: `scripts/e2e-acme.sh` runs pinned Pebble and challtestsrv images and verifies real HTTP-01 through the Gateway, encrypted certificate issuance and distribution, renewal, retry recovery, and revocation. DNS-01 unit coverage verifies all four pinned lego Provider constructors, strict credential loading, challenge selection, persistence, and the no-Gateway-ACK path; live provider tests require operator-owned cloud credentials and are intentionally outside CI.
- HA E2E: `scripts/e2e-ha.sh postgres|mysql` runs three Controllers against one database, verifies active-active API visibility, exactly one Runtime owner, crash takeover, post-takeover Docker health progression, observation advancement, and exclusion of a restarted former owner.

CI runs unit, bounded fuzz, race, vet/static analysis, `govulncheck`, the large Snapshot benchmark, three-store conformance, image build, Compose manifest validation, a real NGINX multi-domain/longest-path/WebSocket/HTTPS-upstream/apply/rollback/LKG test, real Compose, Pebble ACME, PostgreSQL/MySQL three-Controller HA and single-node Swarm E2E suites, SBOM generation, and image vulnerability scanning. The release gate repeats those real E2E suites before publishing artifacts.

The Docker E2E scripts require Linux, `docker`, `curl`, `jq`, and access to `/var/run/docker.sock`; the Compose suite additionally requires `docker compose`, the UI suite requires headless Chrome/Chromium, and the ACME suite requires `openssl`. They always use unique resource names and remove only resources created by the current invocation. The Swarm script never initializes or leaves a Swarm; CI owns that lifecycle.

Volume management behavior and the API contract are described in [volume management](volume-management.md).

## Release artifacts

Tag `v*` builds `linux/amd64` and `linux/arm64` binaries and multi-platform `controller`/`gateway` OCI images. The release workflow attaches checksums, SPDX SBOM and provenance. Image bases must be updated together with [image-lock.json](../build/image-lock.json) and reviewed as a dependency change.

The Controller rebuilds Docker Compose from checksum-pinned upstream source,
applying [a locked dependency patch](../build/compose-dependencies.patch) for
CVE-2026-78669. The plugin reports `v5.6.1-nsc.1`; Docker CLI still comes from the
pinned CLI image. Keep the source checksum, module checksums, image lock and
third-party modification notice consistent when updating Compose. Validate the
bundled plugin as the Controller user, including manifest validation and an
isolated Compose lifecycle, and scan the final image with the existing
HIGH/CRITICAL gate. Upgrading NSC's own Go modules does not update this separate
binary. Remove the local patch only after a pinned upstream replacement has
passed the same checks.

## Vue management console

The frontend was generated using Project Forge 0.3.0 and imported into frontend/; see [provenance](../frontend/PROVENANCE.md). The NSC root is not generator-managed. Node 24.20.0 is a build dependency; production remains a Go executable.

- make frontend-dev starts Vite with a same-origin proxy to 127.0.0.1:8080.
- make frontend-check checks lint, types, API contracts, eight-language catalogs, unit tests and production build.
- make build / make test generate embedded assets first. Build the frontend before invoking Go API tests directly.
- make test-e2e-ui starts an isolated Docker Controller and verifies embedded pages with Playwright.

The frontend `typecheck` and `build` scripts use `vue-tsc -b --force` so existing
TypeScript build metadata cannot skip validation. Local checks, CI, and the
Controller image build use the same complete type check. The Docker context
excludes local build metadata and generated assets.

The deployment wizard loads the selected environment's runtime observations even
when opened from the catalog or project directory. Opening it, changing its
project/environment/runtime, and advancing a step request fresh observations;
an older response must not replace a newer target or the user's draft. Manager
readiness still requires fresh, connected manager evidence and the existing
management checks. Parameter fields and network controls remain in the wizard's
main column, with descriptions below their inputs on desktop and mobile.
`npm run e2e:demo --prefix frontend -- wizard.e2e.ts` exercises this layout and
manager/worker behavior against isolated HTTP fixtures without deploying workloads.

The Vue sidebar owns its active section, including parent sections for resource
details. Runtime render hooks must not add navigation classes directly to the DOM.
Navigation guards keep the admitted section active until navigation succeeds.
`npm run e2e:demo --prefix frontend -- faker.e2e.ts -g sidebar` checks this behavior
with isolated HTTP fixtures, including refresh, history and cancelled navigation.

Shared catalogs live in internal/i18n/catalogs; UI-only catalogs live in frontend/src/shared/i18n/locales. Build-time message compilation supports strict script CSP. Writes use the shared client with ETag, CSRF and idempotency keys; form drafts remain in memory.

## Agent rules and harness

Read [AGENTS.md](../AGENTS.md) and its mandatory [.agents/rules](../.agents/rules). Project procedures live in [.agents/skills](../.agents/skills); neither location replaces higher-priority instructions or authorizes unrelated external actions. Normative documentation is English; localized user manuals and classified historical artifacts retain their language/evidence.

After installing the listed toolchain and running make frontend-install, run make harness-check for repository contracts/static checks or make harness for the full build/unit/coverage gate. These entry points do not install tools or use production fixtures. GO, NPM, NODE and PROTOC can select installed tools; ensure protoc-gen-go and protoc-gen-go-grpc are on PATH. Protobuf verification uses the pinned versions listed above.

Each run saves coverage/harness/<run-id>/summary.json and step logs. Required failures or blocked prerequisites make the run fail; source/baseline changes invalidate its evidence. Full runs include the [coverage gate](test-coverage.md); inspect each fresh report for the actual thresholds and outcome. Race, fuzz and isolated integration suites remain separately required when applicable and are retained in CI.

## Alert routing

See [notification delivery](notification-delivery.md) for receiver/rule separation,
legacy compatibility, deferred reminders and recovery holds. Source transitions
commit to a durable outbox before fanout. The SQL work projection must commit in
the same transaction and reject cross-document dedupe collisions in every store.
Do not replace it with MySQL's unrestricted duplicate-key upsert.

Notification tests cover the production collection and dispatch functions. Queue
claim races synchronize indexed due-work selection; history enumeration is not
part of normal dispatch. Use isolated PostgreSQL and MySQL fixtures for
`TestNotificationWorkAcrossDatabases`, and keep the 10,000-receipt history case
separate from actual external receiver acceptance. Maintenance regression must
cover shared scopes, repeated windows, scope changes, cancellation and resolved
occurrences. External availability still needs independent monitoring.

## Node execution verification

See [durable execution](operation-execution.md) for the frozen-task and mTLS
contract. `TestExecutionAcrossDatabases` requires isolated PostgreSQL/MySQL
fixtures through `NSC_TEST_EXECUTION_POSTGRES_DSN` and
`NSC_TEST_EXECUTION_MYSQL_DSN`. Unix sockets let these private stores participate
in the coverage namespace without granting tests an external network route.
Keep credentials in private environment files and retain the source fingerprint,
actual test outcome and fixture cleanup separately from Docker failover evidence.

Business admission, readiness, issue handling and recovery decisions are described in [operations workflow](operations-workflow.md). The environment overview and activity hub distinguish configuration, current observations and historical success; neither is end-to-end HA acceptance.
