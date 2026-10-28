# Controller failover acceptance

`scripts/e2e-ha-failover.py` provisions an owned PostgreSQL streaming standby or
MySQL GTID replica, three Controllers with separate mTLS identities, and a nested
Docker engine with an observed fixture workload. It does not mount the host
Docker socket into Controllers, change the host Swarm, or inspect business apps.

Build the current Controller image, then run each supported HA store:

```sh
python3 scripts/e2e-ha-failover.py postgres --image nsc-controller:local --output coverage/p0/r04/postgres.json
python3 scripts/e2e-ha-failover.py mysql --image nsc-controller:local --output coverage/p0/r04/mysql.json
```

The fixture requires Docker, Python 3 and the pinned images already available to
the host engine. It uses disposable privileged Docker-in-Docker only to isolate
the Docker workload and observations. All published API ports bind to loopback.
Credential files and evidence are private, and only invocation-owned containers
and networks are cleaned up.

Required assertions are actual database replication, exclusive Runtime lease
ownership, owner crash/restart, observed Docker health transitions, DB-only
network partition while Docker remains reachable, exclusion of the former owner,
and all peers reading committed state after a controlled primary promotion.
The partition explicitly terminates stranded server sessions to model bounded
database keepalive; it does not claim a production network partition has the same
timeout. The recovery deadline is 120 seconds for each bounded wait.

Promotion quiesces fixture writers, waits for the chosen replay watermark,
stops/disconnects the old primary, promotes the standby and switches the writer
DNS alias. It is not proof of lossless unplanned failover of asynchronous
replication. The old primary must remain fenced until it is rebuilt as a standby.

Deployment executor regressions additionally verify heartbeat loss, loss of the
deployment/storage lease immediately after Docker acceptance, Controller
cancellation, and execution deadlines while ownership remains valid. A killed
CLI process or unavailable resource identity observation preserves uncertain intent;
ordinary observed command failures still use the existing compensation path.
The same deployment deadline and ownership watchdog cover rollback and
first-install cleanup. Cancellation or lost ownership during compensation
retains the running operation, encrypted input and current application/route
history; it cannot authorize subsequent cleanup or a terminal failure record.
Claim-attempt fences reject delayed activation, lifecycle writes,
heartbeats and completion even if a restarted Controller reuses its instance ID.
Both successful and failed delayed Docker results retain the replacement claim.
A former owner must issue no further rollback or first-install cleanup,
must retain the encrypted operation input and nonterminal history, and must not
mark an uncertain result successful or failed. The next owner must reconcile
through the existing immutable/idempotent operation path. Business database and
volume data are outside configuration rollback.

The JSON status is `PASS` only after all required fixture checks finish. Earlier
failed runs and skipped external-store tests remain separate evidence. Final
delivery also requires backend coverage, applicable race tests and the harness;
a successful fixture does not replace those gates.
