# Gateway Swarm rollout

This guide describes update and recovery boundaries for an operator-managed global
Gateway. It does not record a particular cluster. Substitute the installation's
service reference at execution time and retain raw inspection data in an ignored,
restricted local report.

## State ownership and update order

Use `stop-first` when old and new tasks share writable generation or recovery
state on a node. `start-first` requires independently isolated writable state and
verified recovery between task lifetimes. Two tasks must not write the same
`current` symlink, `state.json`, NGINX PID file or generated configuration.

A generic conservative policy is:

```yaml
deploy:
  mode: global
  update_config:
    parallelism: 1
    order: stop-first
    failure_action: pause
    max_failure_ratio: 0
    delay: 30s
    monitor: 30s
  rollback_config:
    parallelism: 1
    order: stop-first
    failure_action: pause
    max_failure_ratio: 0
    delay: 30s
    monitor: 30s
```

Review healthchecks, update monitoring, graceful shutdown and rollback against
the tested image and actual state layout. The Agent manages NGINX shutdown; the
service stop-grace period must allow that shutdown to complete. Keep the existing
port mappings and their TCP/UDP publication modes unless changing them is part
of the authorized work.

## Preflight and update

Run on the intended Swarm manager. Capture the service's current specification,
version, image, endpoint, networks, Secrets, mounts, placement, update/rollback
policy, healthcheck and Task IDs in a restricted operator report. Confirm eligible
nodes are Ready/Active, required overlays are healthy, current Gateway connections
are healthy and published listeners have no conflicting owner.

Set the service and tested image references in the operator's environment. A
minimal image-only update is:

```sh
docker service update --detach --no-resolve-image \
  --image "$NSC_GATEWAY_IMAGE" "$NSC_GATEWAY_SERVICE"
```

Distribute or otherwise make the image available to every eligible node first.
An image-only update preserves the reviewed service policy. Do not replace an
existing service with a partial example or an inspection export from another
installation.

## Acceptance and recovery

Observe Service and Task state through the complete update and monitoring period.
Record failed, rejected, paused and unknown results. On completion, verify one
current task per eligible node, the intended image and binary, health/loading
evidence, desired/applied revision agreement and actual HTTP/HTTPS/HTTP3 route
responses as applicable. Compare ports, state, identities and unrelated service
specifications with the pre-update snapshot.

The default policy pauses failed updates and preserves surviving tasks. Retain
failure evidence and require at least two healthy Gateways before rollback so
replacing one still leaves a serving Gateway. Use the capacity-checked helper in
[NSC Swarm upgrades](nsc-swarm-upgrade.md), or, after equivalent manual checks:

```sh
docker service rollback --detach "$NSC_GATEWAY_SERVICE"
```

A command exit or container health does not establish loaded-version evidence or
uninterrupted traffic. Preserve the previous image and configuration until the
recovery check has completed.

The isolated scheduler fixture is
[the Gateway rolling test](../scripts/test-gateway-swarm-rolling.sh). It creates a
nested Swarm with ordinary NGINX and validates task replacement ordering. It does
not initialize or join the host Swarm. Use [real Gateway acceptance](gateway-acceptance.md)
for Agent state, rejected configuration, LKG restart and continuously sampled
HTTP/TLS behavior. Neither fixture proves acceptance of a live installation.

For the standalone global-service stack, local readiness command, capacity-checked
image upgrade script, and real multi-node fixture, see [NSC Swarm upgrades](nsc-swarm-upgrade.md).
