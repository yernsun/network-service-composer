# DevOps OpenAPI

The [DevOps OpenAPI contract](../api/openapi-deployments.yaml) exposes a
machine interface at `/openapi/v1` for project discovery, services, variables,
environment domain drafts and service domain bindings. Deployments use the same
preflight, encrypted review, immutable revision, durable queue, and rollback
path as the console. The administrator first creates the Project, Environment,
and Runtime through the existing control API or console. Global
`management.enabled` and the selected Runtime's `management_enabled` must both
be enabled.

## Project keys

Every signed-in user can change their own password through
`POST /api/v1/me/password` with `current_password` and `new_password` (16–72
bytes). Other browser sessions and the legacy single user Bearer token become
invalid. The current browser receives a refreshed session and CSRF token.
Administrator password resets through `PUT /api/v1/users/{id}` have the same
legacy token behavior. Project API keys remain active after either change.

An administrator can add users and reset passwords in the console or through
the existing `/api/v1/users` endpoints. Administrators and users can manage
their own project keys through `GET/POST /api/v1/users/{userID}/api-keys` and
`DELETE /api/v1/users/{userID}/api-keys/{keyID}`. Issue and revoke requests
require the **operator's current password** in `X-NSC-Reauthenticate`;
revocation also requires the key's `If-Match` ETag. The secret is returned
only on issue. A key is owned by one user and one entire project, including
environments created after issuance. Scopes are immutable; issue a new key
to change permissions. A viewer can issue only read scopes for themselves;
an administrator may explicitly grant a viewer limited write scopes. Disabling
a user or changing their role permanently revokes their keys, even if the user
is enabled again.

```http
POST /api/v1/users/user_.../api-keys
Authorization: Bearer <administrator-user-token>
X-NSC-Reauthenticate: <administrator-password>
Content-Type: application/json

{"name":"release-bot","project_id":"prj_...","scopes":["services.read","services.create","services.deploy"]}
```

Project key permissions are checked on every request against the key, owner,
project, resource ownership and action. Project metadata and environment IDs
and ETags can be discovered without a read scope. Project keys receive only
an environment ID and revision from environment discovery. Other reads require the matching
scope. Service lists are filtered on the server. Variable reads redact secret
values. Read-only scopes do not expose Compose source, logs, or sensitive
domain drafts.

| Resource | Scopes |
| --- | --- |
| Services | `services.read`, `services.create`, `services.edit`, `services.deploy`, `services.operate` |
| Project variables | `project_variables.read`, `project_variables.edit` |
| Environment variables | `environment_variables.read`, `environment_variables.edit` |
| Environment domain drafts and service bindings | `domains.read`, `domains.create`, `domains.edit`, `domains.delete`, `domains.publish` |

`services.operate` permits restart and temporary scale. Persistent scale also
requires `services.edit` and `services.deploy`. A service create or edit scope
permits preflight; final deployment additionally requires `services.deploy`.
The server freezes the derived change types in the encrypted review and checks
the permissions again on confirmation. A candidate that removes an existing
service is rejected. Domain draft mutations and publication have separate
scopes. Sensitive draft reads require `domains.edit`; `domains.read` covers
service binding reads and domain operation status. Operations without a
reliable stored project and environment snapshot cannot be read. There is no
service deletion endpoint or global pool Domain/Route management endpoint in
this OpenAPI.

Creating, editing or deleting a live service domain binding also requires
`domains.publish` alongside `domains.create`, `domains.edit` or
`domains.delete`, respectively. A project key can create a binding only with
an embedded new domain; it cannot attach or modify an existing shared pool
Domain.

Project keys can call `GET /projects`, `GET /projects/{id}`,
`GET /projects/{id}/environments`, and `GET /environments/{id}`. They can then
call the scoped service, variable and domain endpoints listed in the contract.
All writes retain existing ETag, idempotency and asynchronous operation rules.

## Legacy environment keys

An administrator issues a legacy key through `POST /api/v1/openapi-keys` with the
current account password in `X-NSC-Reauthenticate`. The request identifies
one `environment_id` and a name. The response returns `data.token` once;
`GET /api/v1/openapi-keys` later lists only metadata. An active name must be
unique within its environment. Revoke with
`DELETE /api/v1/openapi-keys/{id}`, the key's `If-Match` revision, and the
current password. Revocation is effective on the next request.

```http
POST /api/v1/openapi-keys
Authorization: Bearer <administrator-user-token>
X-NSC-Reauthenticate: <administrator-password>
Content-Type: application/json

{"name":"ci-production","environment_id":"env_..."}
```

Deployment calls use only `X-NSC-API-Key: <issued-token>`. They cannot use a
console cookie or user Bearer token, and the deployment key cannot access
`/api/v1`. The legacy key is limited to one Environment and its deployment operation
history. It does not gain project permissions or access to the new endpoints.
Keep it in a secret manager rather than source files or
request URLs. Each accepted write records the key ID as its actor; request
bodies are hashed in the audit record without storing the key or Compose.

## Install or update a Compose application

1. `GET /openapi/v1/environments/{id}` and retain its `ETag`.
2. `POST /openapi/v1/environments/{id}/preflight` with `compose_yaml` or
   `catalog_app_version_id`, and optional `runtime_env`, `network_name`,
   `deployment_plan`, `base_revision_id`, or `entrypoint`. Inspect the redacted
   result and retain `data.preflight_id`. Preflight does not deploy anything.
3. Within 15 minutes, `POST /openapi/v1/environments/{id}/deployments` with
   `{"preflight_id":"..."}`, the Environment `If-Match` ETag and a unique
   `Idempotency-Key`. Retrying the same request with that key returns the same
   operation. A stale Environment, changed deployment base, or foreign review
   is rejected.
4. Follow the returned `Location` or call
   `GET /openapi/v1/operations/{id}` until the durable operation reaches
   `SUCCEEDED`, `FAILED`, `ROLLED_BACK`, or `ROLLBACK_FAILED`. HTTP 202 and
   `QUEUED` or `RUNNING` do not mean the workload is deployed.

The same full review and confirmation endpoints handle a subsequent Compose
or Stack update. Existing Application revision and data safety rules apply;
an update never infers deletion of omitted Swarm Services from a partial
Compose file.

## Create or update individual Swarm Services

For a Swarm Environment, send `compose_yaml` and `service_mode: create` or
`service_mode: update` to
`POST /openapi/v1/environments/{id}/service-preflight`. The server computes
the affected Service scope, validates ownership and collisions, and retains
omitted Services. Submit its `preflight_id` to
`POST /openapi/v1/environments/{id}/service-deployments` with the same ETag
and idempotency headers. A Compose Runtime rejects this incremental path;
use the full Environment review and deployment endpoints there.

Legacy environment keys can only read operations whose immutable revision source
belongs to their Environment. Project keys use the stored project and
environment scope snapshot. Failed deployments preserve the previous active
revision when rollback succeeds; named volumes and application data are not
rolled back.
