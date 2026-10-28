# Service port publication

The deployment wizard provides per-service TCP/UDP port mappings in step 3 for
image and Compose input. For MySQL, a published port of `13306` and container port
`3306` produces:

```yaml
services:
  mysql:
    image: mysql:8.4
    ports:
      - target: 3306
        published: "13306"
        protocol: tcp
        mode: ingress
```

Supply the database environment and persistent storage configuration separately.
No ports are published until a mapping is supplied. Docker network access and
HTTP domain bindings are independent of port publication; MySQL does not require
a Gateway.

Swarm defaults to ingress, which publishes through the routing mesh on every
node. Host mode accepts traffic only on nodes running a task. A fixed host port
permits at most one task using that port per node. Standalone accepts an optional
bind IP; an empty IP means all host interfaces. Swarm cannot honor `host_ip`, so
the validator rejects it rather than silently broadening access.

The form edits individual mappings through the YAML document AST and preserves
other service configuration, comments and alias consumers. Imported ranges and
automatic ports remain intact until explicitly edited. The form accepts fixed
ports from 1 to 65535; advanced forms remain editable in YAML. A pending form
blocks submission, and changes to the surrounding source prevent applying an
outdated port edit.

Final mappings are read from the server's frozen execution configuration. Port
changes participate in the Compose fingerprint, invalidate review and use the
same incremental service scope as other configuration changes. Backend admission
validates the resolved values at preflight, operation creation and worker
execution, including invalid ports, protocols, network-mode incompatibility and
collisions within the submitted configuration. Generic warning acknowledgement
cannot bypass errors. Dynamic published ranges are accepted for Standalone;
Swarm requires explicit ports or equal-sized short-syntax ranges.
For Docker's legacy Stack parser, numeric strings in long-syntax port mappings
are converted to integers in execution material only; the saved source and
reviewed port values are preserved.

Docker decides runtime allocation during deployment. This feature does not claim
an unused port based on a successful Compose check: live port inventory and host
process listeners are not preflighted. Port conflicts with existing workloads
can still cause a deployment operation to fail, preserving its evidence.

Run `NSC_PORT_ACCEPTANCE=true python3 scripts/e2e-workspace.py` with the documented
workspace fixture prerequisites. Add `NSC_LIFECYCLE_SWARM=true` for a disposable
two-node Swarm. The browser fixture verifies form validation, narrow-screen
layout, frozen review, actual Docker port bindings and TCP responses; it does not
access live business services.

Docker semantics: [Compose ports](https://docs.docker.com/reference/compose-file/services/#ports)
and [Swarm routing mesh](https://docs.docker.com/engine/swarm/ingress/).
