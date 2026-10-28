# Node-local volume management

Open **Infrastructure → Runtime → Resources → Volumes**. Docker runtimes show
that engine's volumes; Swarm runtimes require selecting a node. A volume's
identity includes its node, engine, name and metadata fingerprint. Equal names
on different nodes are different data stores.

Administrators can create plain `local` volumes. Supply a name for a named
volume, or leave it blank for an anonymous volume. Operations run asynchronously;
follow the operation link for completion. If submission fails without a result,
use **Retry the same request**. The retained idempotency key prevents a second
anonymous volume. An unavailable operation result remains pending until it can
be reconciled.

The list distinguishes named, anonymous and unknown types using Docker labels
and actual mount definitions. A random-looking name alone is not proof of an
anonymous volume. Custom drivers and volumes with driver options are displayed
without mutation controls. Mountpoints, arbitrary labels and driver options are
not returned by this API because they can contain private host or storage data.

Use the details panel to copy an external-volume Compose example. Replace the
example image and mount path. The full generated ID also permits explicit reuse
of an anonymous volume. Swarm examples pin a single replica to the original
node. This feature does not move data or weaken application storage admission.
See [Docker volume semantics](https://docs.docker.com/engine/storage/volumes/)
and [Swarm service storage](https://docs.docker.com/engine/swarm/services/).

Deletion is individual and permanent. Both NSC-created and externally created
unused local volumes may be removed by an administrator after acknowledging data
loss. The backend verifies the current node and engine, checks the volume's
fingerprint, and checks all containers (including stopped containers), Service
specifications (including zero replicas), pending application operations and
NSC application recovery records. Worker nodes request live Service specifications
from the provisioned Manager over mTLS; collected reports only locate the Manager. It rechecks the fingerprint before calling
Docker without force. A concurrent Docker container reference also prevents
removal. External administrators should coordinate Service specification changes
with storage maintenance; Docker does not provide an atomic Service-reference
lock with node-local volume deletion.

Stopping an application does not make its retained data disposable. Delete the
application first when intentionally discarding its recovery configuration;
then review the remaining volumes individually. Deleting an application itself
continues to retain volumes. Back up important data before removing a volume.
No bulk deletion or prune action is provided.

Offline nodes and incomplete, outdated or unknown evidence disable changes.
Previously collected metadata may remain visible as read-only evidence; it never
proves that a volume is unused. A missing volume is not automatically recreated
when restarting a stateful application.

A Docker alias on a Swarm manager still checks Swarm Service and application
references. A Docker runtime pointing at a Swarm worker is read-only because
that engine cannot enumerate cluster consumers; use a Swarm runtime with
provisioned nodes instead. Docker runtime aliases with the same endpoint share
the maintenance lock and application-reference scope.

## API

- `GET /api/v1/runtimes/{id}/volumes`: paginated inventory; optional `search`,
  `kind=named|anonymous|unknown`, `usage=used|unused`.
- `POST /api/v1/runtimes/{id}/volumes`: optional `name`, required
  `Idempotency-Key`; returns a durable operation with HTTP 202.
- `GET /api/v1/runtimes/{id}/volumes/{name}`: detail and volume fingerprint ETag.
- `DELETE /api/v1/runtimes/{id}/volumes/{name}`: requires the latest `If-Match`,
  `Idempotency-Key`, and `{ "acknowledge_data_loss": true }`; returns HTTP 202.

Every Swarm request requires `node_id`. Existing session/API-key authorization,
CSRF and management-enable checks apply. Replay of an identical idempotent
request returns its existing operation even if the node subsequently goes
offline. A changed request with the same key is rejected.

## Marketplace data-node selection

The deployment wizard reads nodes independently of template interpolation.
Missing required parameters display inline hints and do not hide the node list.
A single Ready/Active candidate is selected automatically; multiple candidates
remain selectable, with workers listed first. A previous selection is preserved
until changing runtimes; an unavailable selection stays visible and prevents
review and deployment. The backend still checks actual cluster membership and
readiness when reviewing and executing an application.

## Isolated verification

`scripts/test-volume-swarm.sh` creates two disposable nested Docker engines,
loads the already installed `alpine:3.24` fixture image, and checks data retention,
stopped-container and zero-replica consumers, node separation and individual
removal. It removes only the containers and network created by that run.

For the public API and real HA Worker route, build the Controller image and run:

```sh
NSC_VOLUME_ACCEPTANCE=true python3 -B scripts/e2e-market-swarm.py
```

The fixture uses isolated nested nodes, a temporary PostgreSQL database and
independently issued node certificates. `NSC_CONTROLLER_IMAGE` and
`NSC_CONTROLLER_BINARY` may select the current repository-built artifacts.
It verifies generated anonymous IDs and idempotent retries, matching names on
separate engines, immediate rejection of a newly created zero-replica Service
reference, external recreation invalidating the ETag, and individual deletion
through the asynchronous API. The regular market suite also runs these cases.

The mobile browser case is `frontend/tests/workspace/volumes.e2e.ts`. It uses the
isolated Faker service with mocked volume responses; it verifies the UI and HTTP
contract and does not establish real Docker acceptance.
