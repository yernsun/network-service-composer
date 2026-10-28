# Engineering harness

The harness is a shared verification entry point, not an Agent runtime or deployment system. Run commands from the NSC repository root with the toolchain in [development](../../docs/development.md).

- `make harness-check`: repository contracts, harness self-tests, Go formatting/vet, frontend lint/type/API/i18n checks, protobuf reproducibility and official eight-language market validation.
- `make harness`: the same checks plus frontend/Go builds, backend unit tests and backend-service full/incremental coverage gates. The active scope is defined by coverage.json; frontend reports do not block this backend-only admission.
- Existing `make test-race`, bounded fuzz and E2E targets remain separate required checks when applicable; CI retains those jobs. Harness success alone does not prove production readiness or integration coverage.

Install frontend dependencies with `make frontend-install` first. GO, NPM, NODE and PROTOC may select installed tools; NSC_COVERAGE_BASE selects the comparison commit. A missing tool, invalid baseline, failed prerequisite or absent evidence must fail the run. Do not automatically install tools, fetch credentials or relax checks to obtain green output.

Reports live under ignored `coverage/harness/<run-id>/`: a summary records command arguments, start/end times, exit codes, status, log paths, commit, requested baseline and source fingerprint. Independent checks continue after failures; dependent checks are marked BLOCKED. Source changes during a run invalidate its result. Do not place secrets in command arguments. Retain coverage reports referenced by the run alongside its logs.

Backend coverage runs require Linux unshare and ip and execute inside a private user/network namespace with only loopback enabled. Cache Go dependencies before entering isolation. Agent tests use private HTTP/TLS/HTTP3 listeners and command fixtures; their command fixtures do not prove real NGINX syntax or reload behavior. Standalone Agent tests re-execute their fixed-port fixture inside a namespace. A missing isolation tool or denied namespace creation fails verification. Default runs do not deploy, clear data, create a Swarm or access a live Docker socket. External-store tests must use explicitly isolated fixtures. CI owns its ephemeral services and cleanup; resource names must identify the creating run. A skipped or blocked required check cannot be reported as PASS. Cancellation must stop further steps and retain completed evidence.
