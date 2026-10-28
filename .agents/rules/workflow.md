# Development workflow

Work only in network-service-composer. Read the root AGENTS.md and linked rules before editing. User instructions and higher-priority agent instructions take precedence over repository guidance. These rules do not grant permissions or require repeated approval for already authorized work.

1. Inspect the working tree, relevant implementation and existing tests. Preserve unrelated and unfinished user changes.
2. Define the supported behavior, management boundary, acceptance evidence and applicable failure cases. Resolve discoverable facts from the repository before asking questions.
3. Implement a coherent change with behavior tests. Keep discovery separate from ownership, drafts from production targets, delivery from loaded-version evidence, and application recovery from business-data recovery.
4. Run the [harness](harness.md) and applicable integration tests. Review the final diff, generated contracts, documentation and evidence together.
5. Report what changed, checks actually executed, failures, limitations and report locations. Missing evidence is not success. Do not claim deployment or acceptance from a local build.

Never silently overwrite external resources, stale drafts or newer tasks. Unknown execution results must remain unknown until verified. Preserve failed-operation history and recovery/exit paths. Secrets must not appear in logs, diffs or ordinary diagnostics.

Use isolated fixtures for verification. A development request does not authorize unrelated production deployment, data deletion or external communication. Existing explicit authorization remains effective within its scope. Breaking changes are allowed within the approved task; do not advance VERSION unless requested.
