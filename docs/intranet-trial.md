# Swarm intranet trial

Use one noncritical managed application for the first trial. This guide describes
the supported workflow, not an assertion that a particular cluster has passed
acceptance. Preserve the Controller database, encryption keys, application
definitions and Gateway state before updating an existing installation.

## Prerequisites

- Use PostgreSQL or MySQL and a Swarm Runtime connected to a manager. Enable
  application management on both the Controller and Runtime.
- Precreate the Gateway Pool's overlay backend network and connect the Gateways
  to it. Standalone Gateway containers require an attachable overlay.
- Install or explicitly adopt the application's complete definition before
  allowing automatic network changes. Review the deployment diff.
- Provide an intranet DNS record pointing the business hostname at the Gateway
  entry point, or a hosts entry for a limited client test. NSC manages ACME TXT
  challenges when selected, but does not create business A/AAAA/CNAME records.

## Application network

For Stack `demoapp`, the default actual business network is
`demoapp`. Every service joins it, retaining any original networks. An
explicit `network_name` overrides the default. Do not reuse an unrelated
application's network or name the Gateway network here.

The deployment wizard accepts the initial name. The application network panel
can review and deploy a later change. Changes create immutable revisions;
rollbacks restore the historical network selection. Review the affected
services before deployment, since network changes can replace Swarm tasks.

On a service detail page, bind the hostname, path, backend port and protocol.
Only services with desired domain bindings receive the additional Gateway
network. A database with no binding stays on its business networks.

## Choose HTTPS

| Method | Required setup | Maintenance |
| --- | --- | --- |
| ACME DNS-01 | Controlled public domain, configured supported DNS profile, Controller access to the CA and DNS API | Automatic renewal using the saved profile |
| ACME HTTP-01 | Controlled hostname with a port 80 challenge path reachable by the issuing CA | Automatic renewal; keep the challenge path reachable |
| Custom certificate | PEM certificate chain and matching private key covering the hostname | Import a replacement and update the Domain reference before expiry |

DNS-01 does not require the business service or Gateway to be publicly exposed.
A public CA's HTTP-01 validation cannot reach an entry point available only on
the intranet. Select DNS-01 or a suitable custom certificate in that situation.
Clients must trust the issuer of a custom certificate, including an internal CA.

Credential files and DNS profile configuration are described in
[configuration](configuration.md#acme-dns-01). Only the profile reference
appears in the binding form. A custom certificate may be imported from the
binding form before creating the Domain. Certificate selection checks Pool,
validity and host coverage; the server repeats those checks.

Client HTTPS is independent of the backend protocol. A common configuration is
HTTPS at the Gateway and HTTP to the service on its application port.

## Acceptance and recovery

1. Confirm all application services share the selected overlay, retain their
   previous networks, and can still resolve their dependencies.
2. Bind one service and observe the deployment operation. Wait for the route,
   certificate and Gateway loading evidence independently.
3. From an intended client, resolve the hostname and perform an HTTPS request
   with certificate verification enabled. Verify a Gateway-to-service path
   crossing Swarm nodes. An ACK or a successful deployment is insufficient.
4. Verify service replacement, Controller restart, certificate renewal or
   custom replacement, failed issuance, and an explicit retry after failure.
5. Add a second binding, remove one, then remove the last. Only the final
   unbinding should remove the Gateway attachment; business networking remains.

A failed operation retains its error and history. A failed binding can retry
the application's deployment from the service panel. Inspect certificate
errors separately; restarting the application does not repair a DNS credential
or CA reachability problem. Application rollback does not restore business data.

The isolated two-node browser/network scenario is run with:

```sh
NSC_LIFECYCLE_SWARM=true NSC_BINDING_ACCEPTANCE=true \
NSC_BROWSER_BINARY=/path/to/installed/chrome \
python3 scripts/e2e-workspace.py
```

Build current Controller and Agent binaries first. The fixture requires cached
images listed by the script. It creates labelled disposable Docker daemons,
uses a temporary test certificate, verifies HTTPS with that certificate as a
trust anchor, and retains reports in `coverage/workspace-e2e`. It does not use
the developer's Swarm for application workloads. This scenario proves custom
TLS and cross-node networking; ACME issuance/renewal needs its separate tests.
