# Gateway system project

The elected Controller reconciles the explicitly registered NSC deployment into
one protected `Gateway` project and its `prod` environment. This is metadata
initialization, not a Docker deployment, and works with management disabled.
The existing four-node collector remains independent of the leader scheduler.

## Provisioning

Create the cluster's Swarm runtime, then generate the deployment registration
using `scripts/system-project-registration.py`. Pass the runtime ID and each
selected Controller/PostgreSQL service via `--production-service`. The helper
reads the current Manager's cluster identity and resolves each explicit service
to its actual Docker object ID. It makes no changes. Install the resulting object
under `controller.system_project` in every Controller configuration.

The reconciler requires fresh authenticated Manager evidence, a matching bound
node/cluster/runtime, and the exact registered service IDs. Names and labels do
not establish ownership. Existing application-owned services cannot be adopted.
An unavailable report does not prove that a service is absent or stopped.

The project, initial environment, reserved slug and system identity are created
in one transaction. Four simultaneous initializers converge on the unique
system marker. Failed slug claims roll back the transaction. A legacy ordinary
project called Gateway or occupying `gateway` must be resolved explicitly; it is
never silently converted. New ordinary projects cannot use the reserved name.

A newly registered `sandbox` service creates a separate protected `sandbox`
environment on the registered Swarm runtime. Registration does not move or
modify gateways, Sandbox networks, identities, volumes or runtime configuration.
A standalone legacy gateway is never inferred from its name. The built-in
application registration below associates its exact container ID with its
existing local runtime; no replacement workload is required.

Object replacement requires regenerating and installing the registration. Docker
service updates preserving the service ID require no reassociation. Retired
object registrations remain protected history; they do not recognize a new
object merely because it reused a name. Existing discovered service rows and
instance/node observations supply the UI data; no synthetic application or
successful deployment is invented.

## Protection and API

Projects, environments, services and environment references expose `system`.
Project/environment `lifecycle_operations` is empty for system resources and
when management is disabled; these resource capabilities never grant a user
permission. Service lifecycle operations are also empty for system references.

Ordinary project/environment changes, variable writes, deletion, forced deletion,
environment deployment, service mutation and service lifecycle operations cannot
bypass server-side system protection. Storage guards and deletion execution also
check the restriction. Rejections use the localized code
`system.project_protected` with HTTP 403 when the request reaches that guard;
authentication, request validity and management checks may reject earlier.

The deployment reconciler's private context is the only bootstrap write path.
It is not selectable by an HTTP parameter. The existing administrator/global
viewer authorization remains in place; this feature adds no user grants and
adds no credential/source exposure. Project-scoped membership remains separate
from this change.

## Empty workspace

The real and demo controllers share the explicit empty-scope adaptation. Global
aggregate resource lists work with zero visible projects. Missing concrete
resource links keep their error state and URL, with a safe localized heading.
Neither a fallback resource nor the string `undefined` is fabricated. The
original archive and reference templates remain unchanged; generation and
parity verification share the permitted adaptation.

## Rollout and verification

No SQL schema migration, database reset, backup or business-workload recreation
is required. Existing installations are backfilled when the updated leader has
a valid registration and fresh node evidence. Keep deployment registration
consistent across Controllers and retain old image/config references for
investigation. Older binaries do not enforce the new system flag; do not
consider an unreviewed downgrade safe for system-project mutations.

Behavior tests include isolated PostgreSQL/MySQL concurrent initialization,
SQLite metadata transaction fixtures, HTTP protection, and opt-in browser
acceptance (`NSC_SYSTEM_BROWSER=1 go test ./internal/api -run
TestSystemProjectBrowser -count=1 -v`, with `NSC_BROWSER_BINARY` set). Build the
embedded frontend first. Browser fixtures have no usable Docker endpoint and
cannot alter live workloads. Screenshots, traces and database evidence are under
`coverage/system-project/`.

Deployment remains conditional on the agreed verification gates. A successful
local build or database fixture does not imply that the live cluster has been
updated; the iteration report records that separately.

## Built-in applications

Gateway registration can include `applications`, each with a fixed `key`, `name`,
`environment`, `runtime_id`, and explicit `object_ids`. Supported pairs are
`controller` / Controller, `postgresql` / PostgreSQL, `nginx` / NGINX, and
`nginx-sandbox` / NGINX Sandbox. Only the last belongs to `sandbox`; the others
belong to `prod`. Application runtime and environment default runtime are distinct:
joining a project never moves a workload to another runtime.

Use `scripts/system-application-registration.py` on the Manager hosting the
explicitly selected legacy gateway containers. Supply the current Swarm and local
Docker runtime IDs, four Controller services, PostgreSQL service, production
container and Sandbox container. The helper prints IDs without credentials and
performs no writes. Verify that the local Docker runtime is bound to this node.
Replace `controller.system_project` in all four instance configurations with the
result, retaining existing identities and all other settings.

The elected reconciler checks fresh authenticated node evidence and resolves
Swarm objects through service IDs, Docker objects through exact endpoint container
IDs. It atomically persists application rows, environment claims and service
membership under the system project lock. Existing service IDs are preserved.
Missing objects, competing ordinary application ownership and cross-cluster
bindings block reconciliation. Retired associations remain protected history;
replacing a runtime object requires updating deployment registration.

Applications are protected observation records, with no fabricated deployment
revision, Compose source, install operation or active version. They are not marked
as ordinary managed applications. API responses expose `system`, `system_key`,
`environment`, and empty `lifecycle_operations`. An expired application observation
is `UNKNOWN`, not stopped. Normal application/service lifecycle operations,
source writes, project deletion and image policy changes remain forbidden.

Registration migration does not recreate NGINX, PostgreSQL or business workloads,
move data, enable management, expand production gateways or delete volumes.
