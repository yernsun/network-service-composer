# Trial readiness gaps

This is a source and verification inventory for the deployment workspace,
updated on 2026-10-13. A local build or Faker response is not production acceptance.

| Capability | Current implementation | Remaining acceptance or gap |
| --- | --- | --- |
| Deployment wizard drafts | The Controller stores an encrypted, user and environment scoped draft with ETag concurrency control. The same Vue client saves and resumes it against either the Controller or the loopback Faker API. | Verify multi-user recovery and browser failure handling against an isolated Controller. |
| Marketplace install | Review pins an immutable catalog version, rejects caller-provided Compose overrides, and records catalog provenance on the application revision. | The legacy `secretRef` wizard field has no backend contract and is rejected if populated. Catalog variables must be configured through the existing project/environment variable API. |
| Service restore and permanent deletion | A service-specific preview and restore selector is present. Permanent deletion creates a revision without the selected service, removes only its runtime object, retains volumes and history, and disables its routes. The last service cannot be deleted this way. | Verify actual Docker Compose and Swarm behavior, partial failure recovery, and route/certificate preservation in isolated fixtures before production use. |
| Demo isolation | The production Vue bundle has one network API adapter. A separate loopback-only Node Faker serves the same assets and HTTP contract for demonstration and shared browser tests. | Faker covers the tested workspace paths, not every Controller API. Unsupported paths return errors rather than simulated success. Expand fixtures with each newly supported product flow. |
| Port availability | Resolved port syntax and candidate collisions are checked. | Docker detects conflicts with allocated ports during execution. There is no complete preflight inventory of cluster ports or node process listeners. |
| Swarm stateful STOP | The Controller verifies ownership, task/container exit and mount retention before removing service objects. Provisioned per-node collectors provide authenticated, encrypted image/container/volume evidence for a single replica pinned to its original data node. | Remote stateful admission supports explicit named local volumes, not bind mounts or custom drivers. Missing, stale or incomplete scans and same-name replacements block unsafe reuse; they require fresh evidence or explicit operator restoration. See [remote Worker storage](remote-worker-storage.md) and run the isolated retention fixture for the intended build. |
| Compose storage editing | The deployment wizard has storage and placement review. | The existing-source editor lacks the same visual storage classification flow. |
| Image-only deployment parameters | Image deployment generates a small Compose definition. | Complete environment, secret, volume and healthcheck authoring currently requires Compose input. |
| Release gate | Backend coverage, harness, frontend checks and applicable isolated integration runs produce separate evidence. | Failed or unavailable gates must be reported; builds and Faker tests alone do not establish deployment readiness. |

See [frontend/README.md](../frontend/README.md) for the Faker commands and
[compose-incremental-deployment.md](compose-incremental-deployment.md) for
service-scoped deployment behavior. Existing deployments are not migrated by
this work.
