# Real Gateway acceptance

Run the real data-plane acceptance fixture after building the Controller and
Gateway images from the source revision under review:

```sh
python3 -m unittest discover -s scripts -p test_gateway_acceptance.py -v
python3 scripts/gateway_acceptance.py \
  --controller-image "$NSC_CONTROLLER_IMAGE" \
  --gateway-image "$NSC_GATEWAY_IMAGE" \
  --output coverage/gateway-acceptance/current/summary.json
```

Docker, Python 3 and OpenSSL are required. The referenced NSC images and the
pinned PostgreSQL, Node and cleanup images in `build/image-lock.json` must already
exist. Build failure or a missing prerequisite is a failure, not a skipped pass.
The fixture creates its own Docker network and labeled containers. It does not
mount the host Docker socket, initialize or join the host Swarm, or discover
existing applications. Published fixture ports bind only to loopback.

## Acceptance contract

Two real production Gateways have different identities, certificate pairs and
writable generation directories. A third Gateway is a dedicated sandbox. Their
shared leader discovery uses an isolated PostgreSQL database, and their control
connections use Controller identity verification and mTLS. Business TLS uses a
temporary certificate with a concrete `.test` SAN; the client verifies its trust
chain and hostname.

The existing real-NGINX Go integration also checks Agent `Apply` directly with
parser-valid, NGINX-invalid buffer settings. It requires the persisted LKG tuple,
loaded evidence and next-start safety to remain unchanged after rejection. Run
the compiled Agent tests inside the current Gateway image using the same isolated
`NSC_TEST_NGINX_BINARY` / `NSC_TEST_LOCAL_DNS` invocation as CI. Those tests cover
HTTP/3 isolation and TCP fallback separately; they do not establish HTTP/3 rollout
continuity through the test load balancer.

The fixture checks a valid configuration update and a candidate accepted by the
NSC snippet parser but rejected by real `nginx -t`. Failed validation must leave
production traffic and configuration digests unchanged. The sandbox restores its
original empty generation instead of retaining a disposable preview. Successful
updates require fresh matching desired,
applied and confirmed revisions, model digests, rendered/disk digests, loaded
configuration evidence and safe next-start evidence on every production Gateway.
`READY`, a transport ACK or a successful API write alone is insufficient.

The test load balancer passes through HTTP and TLS connections and checks backend
HTTP health. Replacement explicitly drains a Gateway before removing its process,
retains its private state and identity, and admits it only after verifying HTTP,
TLS and the persisted LKG tuple. The other Gateway continues serving requests.
The same immutable image is recreated; this is process/image replacement evidence,
not a cross-version compatibility test. Controller outage also includes a Gateway
cold start from disk before the Controller returns.

HTTP and HTTPS clients sample continuously through each normal scenario. Each
protocol needs at least ten samples per scenario and zero failed samples. Every
sample, including failures and durations, is retained in `requests.jsonl`; the
summary includes scenario windows, counts, loaded-version evidence, source commit,
fixture hashes and actual Docker image IDs. An abrupt Gateway crash has a separate
fault window: failures remain visible, and the LB must exclude the failed Gateway
within five seconds. That bound is a fixture acceptance threshold, not a published
production availability SLO.

Bounded SSE and WebSocket streams check graceful behavior. Existing connections
finish on their original Gateway before replacement; they do not migrate to the
surviving Gateway. This fixture uses HTTP/1.1 and does not establish HTTP/2 or
HTTP/3 availability. It also does not establish acceptance of a production LB,
multi-host overlay network, or storage failure domain.

## Relationship to Swarm verification

[The existing Swarm lifecycle fixture](../scripts/test-gateway-swarm-rolling.sh)
checks task scheduling and replacement order using ordinary NGINX. It remains a
scheduler test. Its request log is not proof that real Agent state, configuration
loading or uninterrupted traffic works. Run it separately when a change affects
Swarm rollout policy, and use the real Gateway fixture for data-plane acceptance.
The [rollout guide](gateway-swarm-rollout.md) describes state ownership and
conservative update ordering.

## Independent load-balancer endpoints

Add `--verify-lb-redundancy` to run the R05 companion case. It creates two separate
LB processes with independent HTTP and TLS listeners, checks business access
through each, terminates the first process, verifies that endpoint is unreachable
and continuously samples HTTP/TLS through the surviving endpoint with zero errors.
The report records distinct processes, failed-endpoint evidence and every request
count. These processes share one fixture host; this does not prove independent
host or zone failure domains, VIP failover, DNS TTL behavior or automatic client
selection of the remaining endpoint. Deploy and test those mechanisms according
to the HA blueprint used by the installation.

## Evidence and cleanup

Reports belong under the Git-ignored `coverage` directory and are written with
mode `0600`. Temporary secrets and diagnostic logs remain in a private fixture
directory on failure; inspect and remove that directory locally after diagnosis.
Do not commit reports, certificates, keys, container inspection exports or logs.
Successful runs remove their temporary state. Cleanup addresses only the
containers and network created by that run.
