# Built-in application market

The market synchronizes at Controller startup. Repeated starts neither duplicate versions nor install or update applications automatically. The seven official applications retain Compose support and provide Swarm service deployments with fixed image digests. The six stateful templates have additional `-nsc.1` versions; earlier versions and installation snapshots remain available.

|Application|Template version|Internal port|Persistence and dependencies|
|---|---|---|---|
|[NGINX](../internal/core/builtin/nginx/docs/en/README.md)|1.31.5|80|Stateless service; no data node|
|[Redis](../internal/core/builtin/redis/docs/en/README.md)|7.4.11-alpine|6379|Named /data volume, AOF and password authentication|
|[MySQL](../internal/core/builtin/mysql/docs/en/README.md)|8.4.11|3306|Named /var/lib/mysql volume; separate root and application accounts|
|[PostgreSQL](../internal/core/builtin/postgresql/docs/en/README.md)|18.6-alpine3.24|5432|Named /var/lib/postgresql volume; initialization account is an administrator|
|[WordPress](../internal/core/builtin/wordpress/docs/en/README.md)|7.1.0-php8.3-apache|80|Dedicated MySQL 8.4.11; database and WordPress file volumes|
|[pgAdmin](../internal/core/builtin/pgadmin/docs/en/README.md)|9.17|80|Account/registration volume; joins an existing PostgreSQL network|
|[phpMyAdmin](../internal/core/builtin/phpmyadmin/docs/en/README.md)|5.2.3-apache|80|Session volume; requires an existing MySQL host/network; MySQL owns database data|

The legacy stateful versions remain Compose-only. Select a `-nsc.1` version for Swarm, then choose a Ready/Active data node. NSC pins all persistent services to that node with one replica and local named volumes. Node loss waits for recovery; no replacement data node or automatic migration is used. NGINX is stateless and needs no data node. Product VERSION is unchanged.

## Installation

1. Select a market version and a Docker or Swarm Runtime with application management explicitly enabled.
2. Review the template, project name and variables. The wizard displays descriptions and defaults. Passwords have no default, require at least 16 bytes and use masked inputs.
3. pgAdmin/phpMyAdmin require an existing database network, such as `mydatabase_default` for Compose or the NSC application network for Swarm; Swarm requires an available overlay network; phpMyAdmin also requires the MySQL Service DNS name. Create the database first if its network is absent. A Gateway network name is not a database address.
4. Run preflight, review differences, compatibility and persistent volumes, then confirm installation. Deployment requires explicit confirmation.
5. After installation, bind HTTPS domains to the WordPress, pgAdmin or phpMyAdmin web service. Prefer a dedicated domain root; suggested domains are examples.

Database templates provide no HTTP domain suggestions and publish no host ports. Access them through Docker networks; NSC HTTP Routes do not carry Redis, MySQL or PostgreSQL protocols.

WordPress database credentials are separate from site administrator credentials; complete WordPress setup on first visit. Sign in to pgAdmin with the configured email/password and register the database manually. phpMyAdmin uses existing MySQL users, stores no automatic-login password and does not enable arbitrary-server mode.

## Updates, data and exit

Passwords and initial database names only apply when initializing empty volumes. Editing template variables does not change existing database accounts. Image/configuration recovery does not restore business data. Back up logical database data and named volumes separately; WordPress needs both files and database. Application deletion retains volumes by default; check versions and accounts before reusing them. Swarm stop requires fresh authenticated node, actual mount and image-volume evidence. Restart verifies the original volume identities on the same node. Stale reports, an offline node, missing/replaced volumes or unknown exit evidence block the operation. Updates, recovery and scaling preserve the frozen node, single replica and mount layout. Configuration rollback does not restore business data.

Template files are in `internal/core/builtin/<app>/`. Use each stateful version's `docs/en/swarm-README.md` and `swarm-operations.md` for its new deployment and lifecycle guidance; the older root README is an immutable installation artifact. Image digests were obtained from official Docker Registry queries, not inferred from tags.

## Verification entry points

- `go test ./internal/core -run TestBuiltinMarketTemplates`: idempotent initialization, pinned images, password/connection requirements, persistent volumes, no host ports, runtime variant selection and legacy Swarm rejection.
- `npm --prefix frontend test -- --maxWorkers=1 --no-file-parallelism`: variable defaults, secret inputs and preserving edits during metadata refresh.
- `python3 scripts/e2e-market.py`: explicitly starts an isolated Controller and installs the six applications with random project names through the real market API. It checks health, authentication, data after restart and database-tool connectivity, then removes only that run's containers/networks/volumes. Build the Controller image first; Docker and image downloads are required.

Upstream documentation: [Redis](https://hub.docker.com/_/redis), [MySQL](https://hub.docker.com/_/mysql), [PostgreSQL](https://hub.docker.com/_/postgres), [WordPress](https://hub.docker.com/_/wordpress), [pgAdmin](https://www.pgadmin.org/docs/pgadmin4/latest/container_deployment.html), [phpMyAdmin](https://docs.phpmyadmin.net/en/latest/setup.html#installing-using-docker).

## Custom static market and eight-language manuals

A single manifest manages the market, Compose, README, environment parameters and manuals. See [format and publishing](marketplace-manifest.md). Russian and Spanish are supported in the UI and backend; official market core documentation covers all eight languages.

The optional `make test-e2e-market-swarm` target uses two disposable Docker-in-Docker
nodes, a private database and an actual Gateway. It installs all seven official
applications through the marketplace API and exercises persistence, HTTPS,
Compose regression and failure boundaries. It requires the repository-built
Controller/Gateway images and cached official template images. Set
`NSC_MARKET_BROWSER=true` to include the real marketplace browser flow (Chromium
and frontend dependencies are required). The fixture removes its
own nodes on exit. It never mounts the host Docker socket inside those nodes.
