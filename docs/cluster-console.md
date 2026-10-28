# Cluster console

`/ui/cluster` adds Overview, Nodes and Containers tabs to the embedded Vue console.
Select an enabled Docker/Swarm Runtime. Tabs, node filters and selected resource
are URL state; node and instance drawers support refresh and browser history.

## Inventory and metrics

The monitor uses only configured Runtime endpoints and their existing TLS
credentials. It does not install an agent, scan node addresses, expose a Docker
API, or modify discovery, routing, service ownership or Gateway configuration.

A Swarm manager supplies all known nodes, services and Tasks. Each configured
Docker daemon supplies its complete local container list, including Compose,
independent containers and stopped containers. Engine ID, Swarm Cluster ID and
Node ID establish identity; hostnames never do. If Docker reports an unspecified
node IP (such as 0.0.0.0), a valid Manager registration address supplies the
displayed IP without changing node identity. Multiple connections to the same
Engine share metric sampling. A Task and its inspected Container are one row.

Containers expand as `Node → Service → Container/Task`. Compose project and
service labels are used verbatim. Swarm stack names come from the service's
`com.docker.stack.namespace` label. Independent containers use a separate group;
unassigned Tasks and services with no instances remain visible. Retained shutdown
Tasks are hidden by default and available through the history toggle. The
historical Task range is whatever Docker still retains, independent of metric
retention.

The server sorts its shared inventory by stable resource ID and paginates it in
50-row pages (maximum 200). Cursors are scoped to the path and filters. Each tree
branch follows its own cursor; opening a drawer never loads the whole inventory.
Existing NSC service references link to the current service details. Other
containers have read-only details; there are no container mutation controls.

The monitor samples every 10 seconds, with at most four concurrent endpoint reads
and eight concurrent container inspection/stat requests. Individual container
requests time out after three seconds; each container collection cycle has an
eight-second deadline. Endpoint inventory requests have an eight-second deadline.
Slow cycles do not overlap, and the sampling order rotates to avoid starvation.
All browser sessions share the observations. Browser polling pauses in background
tabs, but Controller sampling continues to accumulate the short history.

Metrics include container CPU, memory usage/raw usage/limit, cumulative network
RX/TX and cumulative block read/write bytes. CPU uses differences between samples
and can exceed 100% across several cores. The first CPU sample is unavailable.
Memory subtracts `total_inactive_file` (cgroup v1) or `inactive_file` (v2), matching
Docker CLI accounting. Unsupported/missing fields are null, never fabricated zero.
Host cores and RAM are capacity, not current usage. Host CPU usage, load average
and used RAM are explicitly unavailable without a host collector. Remote nodes
without configured direct Docker access have incomplete container inventories
and unavailable container metrics; their Swarm Tasks are still shown.

Each series retains at most 360 samples for one hour. The Controller bounds the
cache to 2,048 container series and 4,096 inspect summaries; oldest series are
evicted when full. Restarting the Controller clears the history. No database
migration or time-series service is added. Errors and collection gaps break the
charts. Last successful values remain marked stale after a failed sample or 30
seconds without a new sample. A stopped container only has retained history.

## API and node labels

All paths below are under `/api/v1/runtimes/{id}/cluster`:

- `GET /`: overview, coverage, source timestamp and capability flags.
- `GET /nodes`: cursor collection; filters `search`, `state`, `label` (key or
  key=value), `node_id` and `history`.
- `GET /nodes/{nodeId}`: node details with a Docker version ETag.
- `GET /nodes/{nodeId}/workloads`: cursor collection for one node.
- `GET /nodes/{nodeId}/instances`: cursor collection filtered by `workload_id` or
  `instance_id`; accepts `history`.
- `GET /metrics?node_id=…&container_id=…&range=1h`: current and retained metrics;
  omitting `container_id` reports unavailable host metrics. Instance membership
  is validated before serving metrics.
- `PUT /nodes/{nodeId}/labels`: complete desired label list, e.g.
  `{"labels":[{"key":"zone","value":"west"},{"key":"empty","value":""}]}`.
  Keys must be nonempty and unique; `{"labels":[]}` removes all labels.

Label mutation requires global management, enabled Runtime management, an enabled
Swarm Runtime, and a live Manager connection. Standalone Engine labels remain
read-only. `If-Match: "rev-<Docker Version.Index>"` is mandatory. Docker's version
check protects the complete NodeSpec; role and Availability are preserved.
Conflicts return 412, missing preconditions 428, and management restrictions 403.
The drawer includes a language selector so modal editing does not block language
changes. The editor shows a diff and scheduling-constraint notice before submitting,
preserves drafts through polling/errors, and never automatically retries writes.
Cookie/CSRF and strict Bearer authentication use existing middleware and audit.
Raw container config, environment values and Docker credentials are not returned.

## Verification

- `make frontend-check`: lint, TypeScript, six-language keys/placeholders, OpenAPI
  drift, unit/component checks and production build.
- `go test ./...` and `go vet ./...`: all package checks. Cluster tests cover
  identities, references, partial coverage, deduplication, metrics, pagination
  beyond 200 items, auth, CSRF, audit and label conflicts.
- `make test-cluster-swarm`: real label add/update/empty/remove against a temporary
  nested Docker daemon. Requires Docker privileged-container support. It refuses
  a non-test daemon, never joins the host Swarm, and removes the test container.
- `NSC_CONTROLLER_IMAGE=<built-image> ./scripts/e2e-ui.sh`: embedded-resource
  browser regression, including 205 instances, lazy retry, deep links, polling
  draft preservation, labels, six languages, theme and insecure HTTP/CSP.

Large lists and browser mutation scenarios use deterministic Engine/API fixtures.
The isolated Swarm test uses the real Docker API; deployment verification should
read the current cluster and existing routing without editing its labels.

## Service instance ownership and freshness


Service totals and instance details use the same selector: observations must
reference the service explicitly, originate from its owning Runtime, and use
Swarm tasks or Docker containers as appropriate. Node container observations
retain their service reference for inspection without contributing a second
instance to a Swarm service. Workload names are not ownership evidence.

Duplicate observations are resolved by source Runtime and instance ID, keeping
the latest before applying the history filter. Stopped Docker containers remain
inspectable; only current running instances contribute to running counts and
unhealthy checks. Genuine rolling-update overlap is not capped to desired size.

Service and application views refresh observation snapshots every ten seconds
while no editor is open. Failed, incomplete or stale snapshots show unknown;
cached live observations also expire after one minute. A node-only update cannot
renew an instance snapshot. Raw observations, historical records and desired
replica settings are not modified by this presentation logic.
