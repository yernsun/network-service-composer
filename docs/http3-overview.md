# HTTP/3 and node overview

The Gateway control plane registers only nsc.gateway.v2.GatewayControl. Health probes use Controller HTTP /healthz and /readyz. The google.golang.org/grpc module version is a Go library version, independent of the application protocol version.

## Domain HTTP/3

Enable HTTP/3 in Domain creation/editing/service binding or submit http3_enabled: true. HTTPS must be enabled and all enabled Gateways in the Pool must report HTTP/3 capability. Managed Domains without certificates remain pending, with no QUIC listener or Alt-Svc advertisement.

HTTP/3 uses UDP 443 while TCP 443 retains HTTPS/HTTP/2. Shared listeners set reuseport once; domains without HTTP/3 explicitly disable negotiation. 0-RTT remains disabled. Backend proxy and control-connection protocols are unchanged.

nginx.http3_advertised_port (NSC_NGINX__HTTP3_ADVERTISED_PORT) specifies the external HTTPS/UDP port, from 1 to 65535. Compose defaults to 443, sandbox to 18443, mapping UDP separately. Operators configure independent-node forwarding; NSC does not modify firewalls.

NSC generates Alt-Svc for HTTP/3 domains with a 300-second lifetime; same-domain custom snippets cannot override it. Custom headers retain context inheritance. Disabling HTTP/3 stops advertisements for new requests; clients with cached advertisements can use TCP.

Migrated domains default to HTTP/3 disabled. Upgrade Controller and Gateway together; renderer is dns-fragments-h3-v4. Gateway validates and retains the previous usable configuration until a complete new configuration passes checks. Retain its state volume.

## Sandbox verification

An HTTP/3-capable sandbox sends real HTTP/3 requests to isolated backends, checking status, headers and protocol without TCP fallback. Optional real-route http3 checks preserve Host/SNI, target the sandbox's local UDP entry and do not follow redirects.

Verify host/independent-node port mappings separately using an HTTP/3-capable curl:

```sh
curl --http3-only --resolve example.test:18443:127.0.0.1 \
  --cacert test-ca.crt https://example.test:18443/
curl --http2 --resolve example.test:18443:127.0.0.1 \
  --cacert test-ca.crt https://example.test:18443/
```

## Overview

The home page reads database totals and cached node summaries from /api/v1/overview, with Runtime detail pages of 50 from /api/v1/overview/runtimes. Refresh runs every 10 seconds and pauses in the background; the home page does not download all Service/Domain lists.

Nodes deduplicate by Engine ID, Swarm Cluster ID and Node ID. Unassigned-instance and zero-instance virtual groups do not count as nodes. The page shows known, Ready, Manager, directly connected and stale nodes plus unobserved Runtimes. CPU/memory represent capacity, not live utilization. Missing/stale observations and incomplete container coverage are explicit; collection failures retain last successful data.

References: [NGINX HTTP/3 module](https://nginx.org/en/docs/http/ngx_http_v3_module.html), [quic-go HTTP/3 client](https://quic-go.net/docs/http3/client/).

## Historical regression environment (2026-09-13)

At the recorded run, the 7080 console was upgraded. The dedicated backend used the following files without host ports or data volumes, leaving existing multi-environment applications unchanged:

```sh
docker compose -p nsc-http3-regression -f test/e2e/http3-backend.yaml up -d
```

The test domain http3.regression.nsc.test was local-only with no public DNS record. Its seven-day certificate was at /tmp/nsc-http3-live/test.crt; this historical path does not imply current validity.

```sh
curl --http3-only --resolve http3.regression.nsc.test:443:127.0.0.1 \
  --cacert /tmp/nsc-http3-live/test.crt https://http3.regression.nsc.test/
```

The sandbox candidate was cancelled and baseline restored after validation. The 18443 test domain was accessible only during candidate validation. To rerun, create a validation task in the NGINX page rather than publishing the draft directly.
