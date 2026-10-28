# Repository privacy

Keep installation-specific deployment records outside Git. Raw Docker inspection
exports, node/Engine/resource IDs, real hostnames and domains, private addresses,
operator account paths, credentials and recovery archives belong in restricted
local storage. Use ignored `coverage/` or an operator-owned directory for working
reports. Review and sanitize extracts before sharing them.

Public examples use reserved names such as `console.example.test`, documentation
addresses such as `192.0.2.10`, generic paths such as `/srv/nsc/state`, and explicit
placeholders for node IDs and Secret references. Standard product paths and
synthetic protocol-test data are not descriptions of an operator's environment.
Keep general deployment procedures in Git; keep actual rollout observations local.

## Commit and push checks

Run `make privacy-check` to inspect tracked and non-ignored working files. Install
the repository's local hooks with `make install-git-hooks`; this sets
`core.hooksPath` for this checkout only. Node from the documented toolchain must
be on PATH (or selected with `NODE`). The pre-commit hook checks the full staged
snapshot, including files force-added despite ignore rules. The pre-push hook
checks outgoing commits, so adding private data in one commit and deleting it in
the next does not bypass the check. CI runs the staged-snapshot check as well.

A new remote branch is checked from its reachable history. For an existing branch,
the remote base must be available locally; fetch it if the check requests it. The
hooks never discard missing history evidence or update remote references themselves.
Object reads disable local Git replacement refs, so a sanitized local substitute
cannot conceal an original blob that would be published. See
[Git's replacement-object documentation](https://git-scm.com/docs/git-replace).

The checks report file paths, line numbers and finding categories, never matched
values. They reject local environment files, private key material, credential
formats, deployment records, ignored report directories, recovery archives,
databases, Python caches, operator paths and installation-only
configuration metadata. They also check documentation/configuration endpoints
against reserved examples and approved public upstream hosts. They are a
conservative publishing guard; reviewers must still identify arbitrary sensitive
values that cannot be recognized reliably by syntax.

Two existing deterministic session/encryption seeds are public disposable-test
fixtures. The guard recognizes only their exact hashes, corresponding key names
and existing CI/E2E fixture paths. Other credential assignments in those files
remain checked; the same seeds in operator configuration are rejected. Never use
public fixture seeds for an installation.

Ignore rules prevent ordinary additions of local files; they do not remove files
already tracked or blobs present in older commits. Docker ignore rules also keep
operator material out of image build contexts. A clean Git archive is the preferred
build context for published images.

## Existing history

Removing or replacing a file in a new commit does not remove its earlier blobs.
History cleanup must be reviewed and coordinated separately: rewritten commits
have new IDs, affected refs need updating, and other clones can reintroduce old
history. Keep any pre-cleanup backup locally, never on a public backup branch or
tag. Preserve original verification evidence locally instead of relabeling it as
new results. Review GitHub pull requests, releases, artifacts and caches separately
when they previously included private material.
See [GitHub's history-cleanup guidance](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/removing-sensitive-data-from-a-repository)
for the effects on other clones and cached references.
