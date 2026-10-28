# Architecture rules

1. Docker events are signals, never the source of truth.
2. NGINX upstreams contain only validated project/Stack-qualified Docker Service DNS names; instance IPs, Task IPs and VIP literals never enter a snapshot.
3. Runtime, health, readiness, and staleness are independent states.
4. Domains, routes, services, and certificates are independent resources.
5. Every reconcile and apply operation is idempotent.
6. State-change publication happens only after a successful database commit.
7. NGINX never accesses Docker or the control-plane database.
8. Every generated configuration passes `nginx -t` before activation.
9. A failed candidate never replaces the last-known-good generation.
10. SQLite is restricted to a single controller and Compose runtimes.
11. Events reduce latency; periodic reconciliation guarantees correctness.
12. Route configuration and backend health are independent. Only an explicitly paused Route returns an NSC-generated 503; DNS/connect failures remain 502/504 and application 503 responses are preserved.
13. Certificate operations are asynchronous, persistent, and lock protected.
14. Gateway agents consume pool-scoped snapshots rather than database entities.
15. A gateway pool defines a network-reachability boundary.
16. Control-plane failure never removes the active data-plane generation.
17. Service-DNS probe results are fenced by leader epoch, monotonic sequence, and a per-target token derived from DNS name, port and policy; probe results never add or remove NGINX peers.
18. Conflicting discovered TLS modes disable only the conflicting route and emit a diagnostic; they never make an existing domain alternate between modes.
19. Runtime observations, offline transitions, and lifecycle sweeps are fenced by one durable issued/applied observation epoch so a superseded owner cannot overwrite newer state.
20. Every non-bootstrap generation durably records its Pool identity beside the configuration; an Agent refuses to start or apply an LKG belonging to another Pool.
21. An ACME certificate keeps its challenge type and non-secret DNS Provider reference for its entire renewal lifecycle; DNS API credentials exist only in Controller memory after loading from a read-only Secret file.
22. Localized human text is not an API machine contract: clients use stable Problem `code`, `params`, and JSON Pointers; raw infrastructure diagnostics are never machine-translated.
23. Docker mutation requires both the global and Runtime management gates, and only explicitly managed Applications may receive lifecycle operations.
24. Application revisions are immutable and encrypted; deployment fails closed when any protected input cannot be decrypted.
25. A project/stack has one deployment lease, one in-flight Application operation, and a stable service key independent of Docker object replacement.
26. Candidate deployment failure restores the previous active revision; first-install cleanup never removes named volumes or images.
27. User Compose is immutable. NSC-generated networking is a separate override whose Route stays PENDING until deployment succeeds.
28. Catalog versions are immutable, pinned and digest-verified; discovery of a newer version never deploys it automatically.
29. Pool config revision changes only for Domain, Route, Service DNS identity, backend transport or certificate configuration; instance recreation, Task IP changes and scaling affect status/probe revision only.
30. The Gateway protocol is strictly `nsc.gateway.v2`; mixed Controller/Gateway versions are rejected as incompatible.
31. `/etc/nginx/nginx.conf` is fixed. Immutable generations publish one fragment per Domain and one per shared upstream, then atomically switch `current` only after `nginx -t` succeeds.

## Dependency direction

```text
runtime -> model
storage -> model
discovery -> runtime + storage
application -> model
deployment -> application + runtime + storage
gateway -> storage + model
nginx -> gateway snapshot
api/ui -> core application services
```
