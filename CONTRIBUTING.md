# Contributing

Read [AGENTS.md](AGENTS.md) and the linked engineering rules before changing
business behavior. Discuss substantial product scope changes in an issue. Keep
patches focused, preserve existing failure evidence and add tests for ownership,
authorization, asynchronous execution and recovery boundaries.

Run `make frontend-check` for console changes, `make test-coverage-backend` for
backend changes, and `make harness` plus applicable integration checks for broad
changes. Use isolated fixtures and runtime-generated credentials. Never include
operator configuration, private keys, absolute personal paths, raw deployment
logs or database backups in a patch. Follow the [privacy policy](docs/repository-privacy.md).

Current documentation is English; interface changes cover all eight supported
locales. Historical records preserve their original dates and outcomes. Generated
contracts must match their sources. Contributions are accepted under the project's
Apache-2.0 license; retain third-party copyright and license notices.

Coverage thresholds remain backend branches 80%, frontend branches 50%, and
incremental backend statements and branches 90%. Only a verified, clean initial
commit with no parents establishes the full-coverage baseline: its incremental
result is **NOT_APPLICABLE**, never a fabricated passing percentage. Normal
commits with unavailable comparison history fail admission.
