# Repository rename handoff

The product name is **Network Service Composer**, the full name is **Network
Service Composer for Nginx**, and the abbreviation remains **NSC**. The target
repository is [yernsun/network-service-composer](https://github.com/yernsun/network-service-composer).
The source is prepared for that address before the GitHub repository is renamed.

## GitHub handoff

After merging the rename change, rename the GitHub repository to
`network-service-composer`, then update each local clone:

```sh
git remote set-url origin git@github.com:yernsun/network-service-composer.git
git remote -v
```

The Go module is `github.com/yernsun/network-service-composer`; external Go
consumers must update their imports. Clone instructions and external integrations
must use the new address. The local checkout directory may retain its current
name; host bind mounts must refer to the actual directory on that host.

Run CI and manually dispatch the marketplace Pages workflow after the rename.
That workflow derives its default endpoint from Pages and its homepage from the
current repository. Review repository variables `MARKET_ENDPOINT` and
`MARKET_HOMEPAGE`: update explicit values containing the old repository name.
Update configured marketplace endpoints wherever the Pages address changes.
Check Pages settings, external badges, webhooks and repository references outside
this checkout. Confirm the NSC GHCR packages' repository association and Actions
access under the renamed repository before the next requested release.

Release binaries (`nsc-controller`, `nsc-agent`), release archives (`nsc-*`) and
images (`nsc-controller`, `nsc-gateway`) retain their NSC names. No product version
is advanced by this change, and no release or deployment is performed automatically.

## Runtime identifiers

This is a breaking rename without a compatibility or data-migration layer:

- The default Compose project is `network-service-composer`. Its default container,
  network and volume names differ from those of `nginx-service-composer`. Running
  the new defaults does not migrate old volumes or stop old containers. Plan an
  explicit deployment/data handoff before using them on an existing installation.
- Application network ownership and revision labels use `io.network-service-composer`.
  Networks with only `io.nginx-service-composer` labels are rejected for reuse and
  retained during cleanup; they are not automatically relabeled or deleted.
- The demo workspace storage key is `network-service-composer-vue-v1.3`. Old demo
  state is not imported. NSC preference keys retain their existing NSC names.
- New PKI certificates use the new product organization name. Existing certificate
  bytes are not rewritten; NSC protocol identities remain unchanged.

NSC environment variables, binary names, configuration/state directories, API
routes and protobuf package names retain their existing contracts. The ACME
client User-Agent uses `network-service-composer`.

## Preserved references

Dated historical reports retain original deployment names and evidence. The
original `nginx-service-composer.tar.gz`, frontend reference files and classified
immutable built-in README artifacts retain their original bytes. Provenance
documents continue to identify those inputs. The frontend archive comparison
applies only the explicit branding adaptation in `frontend/scripts/brand.mjs`.
The tracked host mount specification retains its real checkout path.

`scripts/check-brand.mjs` rejects old product/module names outside these declared
exceptions and the regression fixtures that verify rejection of old ownership labels.
