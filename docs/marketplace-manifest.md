# Static marketplace and multilingual documentation

The NSC v0.1.0 community candidate includes `STATIC_MANIFEST` application sources. Maintain `internal/core/builtin/market.yaml` to describe the market, seven applications and README/operations manuals in eight languages. GitHub Pages serves the build output without a backend. Existing `GIT` JSON index sources remain supported; published built-in installation materials retain their compatibility baseline.

## Publish

```sh
go run ./cmd/nsc-market \
  --manifest internal/core/builtin/market.yaml \
  --official \
  --endpoint https://YOUR-ACCOUNT.github.io/YOUR-MARKET/ \
  --homepage https://YOUR-WEBSITE/ \
  --output dist/market
```

`endpoint` is an HTTPS file base URL ending in `/`; `homepage` is a separate official website link. Example URLs are not automatically registered as remote sources. Upload the entire output directory: the entry point is `endpoint + market.yaml`, and all referenced `objects/<sha256>` files must accompany it. Stored NSC snapshots retain their required materials independently of later remote availability.

`.github/workflows/marketplace-pages.yml` provides manual publishing. Enable the GitHub Pages Actions source and optionally set `MARKET_ENDPOINT` and `MARKET_HOMEPAGE`. Defaults are the Pages base URL and repository page. If Pages already hosts a website, merge market assets into that site's deployment directory to avoid replacing the website. See [GitHub Pages custom workflows](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages).

## Maintain the manifest

Use YAML (JSON is also valid YAML), with `schema_version: 1`. `marketplace` contains `id`, localized `name`/`description`, `endpoint`, `homepage`, `default_locale`, `locales` and `docs`.

Each `apps` entry has a stable `id`, localized names and `versions`. A version requires `version` and `files.compose`; optional entries include `files.swarm`, `files.env_example`, locale-to-path `files.readme`, stable document-ID-to-description `files.manuals`, and path-to-image `files.assets`.

Document descriptions contain localized `title`, `files`, `revision` and `translations`. `revision` is the default-language source SHA256; `translations[locale]` acknowledges the source SHA256 reviewed for each translation. README uses `files.readme_revision` and `files.readme_translations`. Source changes block the build until translations and their acknowledgements are updated. These markers record maintainer review, not automated translation-quality proof.

Relative paths resolve from the manifest directory. For another public Git repository, specify the following on the application version:

```yaml
source:
  repository: https://github.com/OWNER/REPOSITORY.git
  ref: main
  directory: apps/redis
```

The publisher resolves the ref to a commit, reads files from that directory and copies them into static artifacts. It does not execute repository scripts, hooks, submodules or LFS. Publishing requires Go and Git in a controlled environment; NSC itself does not clone repositories. Limits: 4 MiB per file, 32 MiB total and 1000 files.

`variables` reuse application parameters with localized `description`. Precedence is user input, manifest defaults, then env examples. Secret fields cannot have usable defaults or values in env examples. `recommended_domains` reuse existing entry suggestions; loading a template does not establish Runtime compatibility, which still requires preflight.

The initial Compose path uses one definition file. Build directives, external file dependencies and unsupported fields are never silently discarded. Multi-file Compose merging, private Git credentials and arbitrary remote file assembly are outside this scope.

## Connect NSC

Choose `STATIC_MANIFEST` when adding a source, enter its name and HTTPS manifest URL, and explicitly trust the source. Usually the confirmed file endpoint can be omitted; provide it explicitly when it differs from the manifest directory. Legacy JSON indexes still require a pinned commit or digest.

Synchronization verifies every declared SHA256 and updates atomically only after success. Originals and translations are stored in version snapshots and readable in details and installation flows. External images do not load automatically and raw HTML does not execute. Internal manual links stay in the reader; website links open separately.

Changing an existing version's content is rejected; publish a new version. Removed versions become withdrawn and cannot be newly installed; history and existing applications remain. Synchronization never updates workloads. Endpoint changes pause synchronization until an administrator confirms the new address.

## Eight languages

The shared authority is `internal/i18n/locales.json`: en, ja, de, zh-CN, zh-TW, fr, ru and es. Backend, frontend and official market builds consume it. `--official` requires core manuals and each application's README and operations manual in all eight languages, with matching source-revision acknowledgements.

Third-party markets may declare fewer languages. Selection uses the current locale, an available base language, then the market default; fallback displays the actual language. Switching locale preserves document identity. Missing document translations do not change the interface language.

## Verification

Run `go test ./...`, frontend build/tests and the official build above. Cover missing files, stale translations, invalid internal links, digest mismatches, version conflicts and withdrawal protection. Validate an actual installation using a test Runtime before production use. This database migration retains data and does not advance the product version.

## Swarm variants and storage

`files.swarm` references an independent, digest-verified Swarm template. NSC selects it for Swarm runtimes throughout validation, review and execution; existing catalogs without this field continue compatibility analysis of their Compose template. A new variant requires a new market version. Both definitions contribute to the frozen artifact digest.

The official stateful variants use one replica, local named volumes, stop-first updates and rollback, and a selected Ready/Active data node (`deployment_plan.data_node_id`). WordPress and its MySQL database share that node. Database tools require an existing overlay network. Database ports are not published to the host. NGINX remains stateless. Existing Compose installations are not migrated.

The application revision freezes placement and mounts. Stateful scaling, node changes and volume replacement are rejected. Node loss waits for recovery; there is no automatic data migration. Authenticated node observations prove image-declared volumes, actual container mounts, volume identities and task exit before stop. A durable retention record must match the original volumes before restart. Missing, stale or unknown evidence fails closed. Deletion retains data volumes. Docker configuration rollback does not roll back business data; operators must maintain database backups and WordPress file backups.
