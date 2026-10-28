# Community development and acceptance record

> Historical record. Results, versions, locale counts and temporary evidence paths describe the dated run, not current deployment or acceptance. See [current development guidance](development.md) and [coverage status](test-coverage.md).

Date: 2026-09-13. Based on Community requirements and interaction closure 1.0; product version remains 0.2.0. This records a development increment, not completed G0/G1/G2 acceptance.

## Implemented increment

|Area|Implementation and evidence|Boundary at the time|
|---|---|---|
|Identity|Durable local administrator/global viewer, password hashing, independent token rotation, revision-based session invalidation and backend write rejection|Initial identity imported once from installation configuration; no initialization wizard or organization RBAC|
|Gateway evidence|Separate new-request loading evidence, disk digest and startup checks; unknown after three refresh periods, retaining last confirmation|Does not prove all paths, all nodes behind shared ingress or existing long connections|
|External configuration protection|Agent checks managed-generation disk digest and main configuration before mutation; external changes block overwrite|No arbitrary native-config adoption or automatic manual-config repair|
|Snippet publication|Drafts do not change targets; fixed production targets, encrypted complete candidate, sequential execution, stop on failure/unknown, durable per-target outcomes and stop/verify actions|Snippets only; not all Route/certificate/discovery changes; incomplete range selection, retry, accept-current-state and recovery workflows|
|Application boundaries/recovery|Core API and worker reject unadopted writes; recovery preview binds current baseline and creates a revision after data-boundary acknowledgement; secrets and pinned images retained|No volume/database recovery; compatibility scanning and full adoption/exit acceptance incomplete|
|Issues/activity|Durable issues, paginated activity and login events; acknowledgement differs from resolution; Gateway/snippet/certificate issues|Incomplete Runtime/target-loss coverage and external notifications|
|UI/contracts|Users, activity/issues, Gateway evidence, per-target publication and application recovery; six interface languages and synchronized OpenAPI types|Only listed increments tested; AC-01 through AC-44 not fully accepted|

## Compatibility and installation notes

- Internal handshake capability changed to 3 while the protobuf package remains v2. Build/update Controller and Agent together; old Agents lack evidence capability. Product/frontend version fields did not advance.
- This increment did not clear databases, delete runtime resources or deploy production. Breaking-change authorization does not automatically reset user data.
- Initial credentials import only into an absent identity store. Later installation-token changes do not overwrite persisted identities. Password reset revokes API tokens; token rotation invalidates old sessions.
- New state uses existing control-document storage; publication snapshots and application recovery materials use existing envelopes and participate in key re-encryption.
- Source code uses the root Apache-2.0 LICENSE; dependencies retain their licenses.

## Verification in this increment

These results concern that workspace and isolated fixtures, not an updated deployment.

- Full go test ./... passed, including identity, viewer denial, session revocation, fixed targets, stop-on-failure, unknown/expired evidence, issue acknowledgement and historical recovery tests.
- Frontend ESLint, 28 Vitest tests, TypeScript and production build passed.
- Go race checking did not execute successfully: the environment lacked a C compiler and failed during build even with CGO enabled. This was not a concurrency-check pass.
- Isolated Controller + Chromium Community scenarios passed: administrator creates viewer, viewer reads activity, user-page read-only notice and server-side write rejection.
- The Agent test binary mounted into local nsc-gateway:v0.2.0 (NGINX 1.31.5) passed real HTTP/TLS/WebSocket, invalid-candidate rejection, loading/startup evidence, external-change protection and cold-start LKG tests.
- Real tests used --network none and NSC_TEST_LOCAL_DNS=true; DNS fixtures returned loopback for localhost only. The first attempt without fixtures failed on localhost DNS, not product loading. An IP-based attempt was rejected by existing validation; product validation was not weakened.

Reproduce the real NGINX test:

```sh
CGO_ENABLED=0 go test -c -o /tmp/nsc-agent-tests ./internal/agent
docker run --rm --network none \
  --sysctl net.ipv4.ip_unprivileged_port_start=0 \
  --entrypoint /tmp/agent.test \
  -v /tmp/nsc-agent-tests:/tmp/agent.test:ro \
  -e NSC_TEST_NGINX_BINARY=/usr/sbin/nginx \
  -e NSC_TEST_LOCAL_DNS=true \
  nsc-gateway:v0.2.0 -test.run TestGenerationManagerWithRealNGINX -test.v
```

## Remaining delivery work

These approved requirements remained incomplete despite the increment's unit/browser passes.

1. P0-01/02: initialization wizard, Runtime identity deduplication/capability preflight, default observation, resource adoption lifecycle and conflict resolution.
2. P0-04/05/06: Route/Domain drafts and fixed publication, shared discovery/certificate scheduling, per-Gateway goals/recovery/stale-task isolation, certificate asset/deployment separation and target-side reachability evidence.
3. P0-07: complete compatibility matrix, external-application drift protection, partial/unknown replica updates and dependent-ingress recovery.
4. P0-08/09: remaining sensitive-read review, issue types, controlled diagnostics/export, paused backup recovery, exit verification and deletion protection.
5. P1: automatic compatibility scanning, read-only migration analysis, external-change drafts, dependency diagnostics, basic notifications and full editing assistance.
6. Execute AC-01 through AC-44 against fixed builds and record actual G0/G1/G2 gates; unpassed stages cannot be advertised as publicly ready.
