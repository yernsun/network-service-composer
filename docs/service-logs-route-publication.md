# Service logs and Pool route publication

The Service logs tab follows Docker Swarm `ServiceLogs` over SSE. Only administrators can open it. The Controller requests the Swarm Service ID from a manager; it never reads container logs or stores log lines. `node://` Runtimes use the existing mutual-TLS Controller node channel and require the current node owner. Compose services show an unsupported state. A stream ends after 30 minutes and can be resumed from the latest 200 lines; Docker log-driver support still determines whether Service logs are available.

Managed domain edits are saved in the Pool Nginx draft. Saving does not change the running Gateway configuration. Validation renders the candidate built from current production plus the draft and asks every online Gateway in the Pool to test it. Publishing compares the draft and Pool baseline, then commits the routes, snippets, release record, and target revision in one transaction. Each Gateway's load result remains visible in the release record; partial or unknown results require investigation. Custom HTTP(S) proxy targets can reach addresses accessible from the Gateway and configuration validation is not an application health check.

The domain History tab reads Pool publication versions and saved draft revisions from the Controller. Each new draft save writes its history snapshot in the same database transaction, so a failed history write rejects the save. The `/api/v1/gateway-pools/{id}/nginx/draft-versions` endpoint pages through these snapshots; both draft and published histories retain the most recent 100 records, subject to protection of the active published version. A current draft saved before this feature remains visible, but its earlier draft contents cannot be reconstructed. A published version means the Controller committed the release; inspect Gateway load evidence before claiming every target is running it. The proxy editor defaults to an internal Service and requires an HTTP(S) URL when switched to a custom target.

## Upgrade order

1. Apply storage migration `00013_route_publication.sql` in the configured database, after all earlier migrations.
2. Upgrade every Controller, including each configured Swarm manager node, then upgrade the Gateway components. The renderer protocol version changes with route support. A mixed Controller/Gateway rollout may temporarily prevent publication.
3. Check `/api/v1/gateway-pools/{id}/nginx`, the Pool gateway evidence, and a draft preview. Confirm that every intended Gateway is online before validating and publishing. A Pool publish can interrupt service while the Gateway loads Nginx.
4. In a Swarm service, open the Logs tab as an administrator and verify a live stdout/stderr line. Check that a Compose service and a viewer cannot start the stream.
5. Save a fixed response such as `/hello`, validate it on all Gateway targets, publish, and inspect the release targets for confirmed load results. Repeat for redirect and proxy routes when used.

Existing direct `/routes` writes remain available for Service-backed routes. They advance the Pool revision and invalidate older validations. Existing uncommitted work in other areas is unrelated to this rollout.
