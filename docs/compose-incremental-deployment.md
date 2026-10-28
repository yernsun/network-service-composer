# Compose source and incremental Stack deployment

The Compose workspace presents historical JSON as block YAML without changing the encrypted historical source. Raw downloads retain the original bytes. Existing YAML remains editable with its comments and anchors. Draft writes use an ETag and the application/source revision; a conflict retains the editor contents until the user reloads the comparison baseline.

The workspace compares deployed content or a saved draft with current edits. Ordinary diffs mask environment values, command literals, extension payloads and comments. Configuration summaries still identify services with confidential changes. A final review compares the server's resolved, image-pinned submission against the deployed service definitions. Editing invalidates that review.

## Stack ownership and execution

An environment continues to own one Application/Stack. Creating an image service uses its application identifier as the Compose service key. Service preflight derives a complete service submission, rejects create-name collisions, checks Docker ownership/version observations, and stores an encrypted, expiring review. Submission cannot enlarge that frozen scope.

The worker submits only the selected services and referenced resources. It does not pass `--prune`. Successful activation preserves other services, bindings and replica overrides. The new immutable aggregate revision contains the retained configuration plus the accepted service revisions; the original source revision remains unchanged. Partial or unknown execution failures retain their evidence instead of rolling back the entire Stack. A fresh review is required before retrying a failed incremental update.

Project slug is the default business network name for new services. The worker checks network type and ownership and declares the overlay as external. Existing service attachments remain unchanged. An edit to a shared resource expands the review to include the complete deployed definitions of services that use it. Runtime environment changes similarly re-review existing service definitions.

Service source/draft endpoints provide independent encrypted drafts. Whole-Stack source remains available. Restore of a historical revision containing a service scope restores those services; restore of pre-incremental history retains later additions. Restart and temporary scale operate on the selected service, with zero replicas bypassing storage placement admission. Temporary scale does not save a replica override, so the saved replica count returns after the next application deployment. Global and job-mode services cannot be scaled. Persistent scale requires the current Service ETag, edits only the selected service's `deploy.replicas`, creates an immutable aggregate revision and deploys only that service. It clears any old override for that service while retaining other overrides. When the Application is stopped or down, the new revision waits for an explicit start. A failed scoped publish retains the active revision and failure record; a new request can review current state and retry. A failed domain network operation can be retried independently of application deployment.

`remove_runtime` requires the current Service ETag and removes only the selected Docker Service or Compose containers. It does not pass a volume-removal flag. The managed Service record, Compose source, volumes, domains, certificates and bindings remain; the destination is unavailable until explicit application redeployment. Repeated removal succeeds if the runtime object is already absent. Omitted YAML services do not imply deletion. A service-specific historical restore selector is not provided by this workspace.

## Entrypoints

Internal deployment does not require a Gateway. For Swarm domain deployment, Gateway options come from the target runtime Pool, subject to environment restrictions. An existing matching certificate can be selected. A durable entrypoint intent is reconciled after the service becomes ready; its operation status and bounded retries are independent of the browser session. The process does not modify DNS.

Entrypoint identities belong to services and survive persistent scaling and a
successful start or redeploy after an earlier failed deployment. Immutable
revisions retain the per-service intents; failed operation history remains
unchanged. Coordination requires a confirmed deployment and fresh, complete
runtime membership evidence. Restored or stale counters do not authorize ingress
publication.

The binding, network task and intent receipt commit together. An existing owned
binding is reused only when its host, root route, backend and TLS settings match.
Conflicting settings require explicit binding review. Network retries advance
the receipt and task atomically while retaining the accepted workload revision.
Swarm reapplies only the bound service. Compose reapplies the frozen active
project and rejects an attachment if a newer aggregate has become active.
Tasks recheck the active service version and intent before Docker commands;
unapplied desired revisions are not ingress execution inputs. Explicitly
disabled bindings remain disabled. Changing an entrypoint does not implicitly
delete previously published domain bindings.

Legacy operation receipts are adopted idempotently by background coordination.
Status reads perform no migration writes. Project deletion archives current
intent receipts, and recovery barriers prevent their coordination.

Deleting a service removes its image locks, tagged-image metadata, service head
and entrypoint intent from the new revision. Other service definitions, shared
networks and volume declarations remain; failed and historical revisions are
immutable. Deletion readiness requires a complete runtime observation after
the operation finished and proof that the frozen runtime object is absent.

Readiness uses the operation's own service scope. Lifecycle actions do not
inherit a prior incremental submission's scope. For a service operation, the
current service head and relevant target configuration define the baseline;
an unrelated service update does not invalidate an unchanged target. Whole
application operations still compare the complete accepted revision.

Operation responses include an additive `business_action` derived from the
accepted revision, consistently across submission, detail and list endpoints.
It distinguishes persistent scaling, service deletion, service/application
restore, and service creation/update. The executor `type` is unchanged; missing
or unreadable historical metadata falls back to that type rather than guessing
that every service upgrade is scaling. Later scaling, deletion and image updates
clear inherited restore provenance on their new revision and preserve history.

The entrypoint panel refetches on entry and uses uncached, cancellable reads.
A failed refresh shows an unknown current state and labels any retained result
as history with its last successful refresh time. Permission failures hide
cached receipt details. Retry controls require a current failed receipt, and
both accepted retries and stale-ETag rejections refresh state. Shared mutation
invalidation refreshes related inventory without overwriting deployment drafts.

## Verification and remaining admission

The isolated browser fixture is `frontend/tests/workspace/incremental.e2e.ts`, selected with `NSC_INCREMENTAL_ACCEPTANCE=true`. It verifies an approximately 85 KB JSON source, unchanged original source and Docker service ID/Spec/Version/tasks, two replicas of the added service, the project overlay, create-name conflict, and desktop/mobile diffs. `NSC_INCREMENTAL_DOMAIN=true` adds Gateway/certificate selection and HTTPS verification in the disposable binding fixture.

Run the repository harness, backend coverage, frontend coverage, applicable race/store checks, and the isolated browser fixture against the final source. The Compose and Swarm management fixtures also exercise temporary scale reset, persistent scale, runtime removal, retained bindings and redeployment recovery. Reports under `coverage/` describe actual results; this document is not a production deployment or acceptance claim.
