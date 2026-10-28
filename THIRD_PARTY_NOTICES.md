# Third-party notices

NSC's own source is Apache-2.0. Third-party components retain their original
licenses and notices. Redistribution must retain the applicable license texts;
the NSC license does not replace them.

- [Dependency inventory](third_party/dependencies.json) records the pinned Go
  module graph and npm lockfile, scopes, declared npm licenses, notice copies and
  unresolved records. The inventory is deliberately broader than the linked
  binary or browser bundle. Optional npm packages absent from this platform are
  listed but are not asserted to be distributed.
- [Source dependency SBOM](third_party/sbom.cdx.json) is a CycloneDX inventory.
  It does not describe an image's operating-system packages or assert that every
  listed dependency is linked into a binary. Final image SBOMs must be generated
  from the exact image digests.
- [Bundled frontend inventory](third_party/bundled-frontend.json) records packages
  with rendered modules in the production Rollup output, without local paths.
  Build-only packages are distinguished from this redistributed browser code.
- [License copies](third_party/licenses) preserve dependency notices verbatim.
  Generate these with `node scripts/release/licenses.mjs` after installing the
  pinned dependencies. Unresolved source/license provenance blocks publication.
- [HTMX license](third_party/htmx/LICENSE) applies to the retained HTMX resource.
- Historical Project Forge scaffold attribution is retained under
  [Project Forge MIT license](third_party/project-forge/LICENSE), retrieved from
  commit `a36fb96da3780b4bb8086cbbdb803e08ec163457` of
  [Project Forge](https://github.com/yernsun/project-forge).
- The embedded Compose schema originates in
  [compose-spec](https://github.com/compose-spec/compose-spec), licensed under
  Apache-2.0. The pinned revision and source digest are recorded in
  [schema provenance](internal/composecheck/SCHEMA.md); its license copy is
  [SCHEMA-LICENSE](internal/composecheck/SCHEMA-LICENSE).
- Restored frontend templates have their original
  [provenance record](frontend/PROVENANCE.md). Possession of an archive is not
  proof of redistribution permission; confirmation of its source license is a
  release prerequisite. Fonts named in CSS use client-side system fallbacks;
  no font-file redistribution is claimed.
- Marketplace templates reference independently distributed application images.
  NSC does not bundle those application images; their use and redistribution
  remain subject to their publishers' terms. Template/source notices still apply.

Container packages additionally include NGINX, Docker CLI/Compose and the base
distribution's packages. Retain their upstream notices and generate an image
package inventory before publication. A source dependency inventory alone does
not establish container redistribution compliance.

Pinned Docker CLI and Compose top-level licenses are retained in
[container tool notices](third_party/container-tools/sources.json). Their binary
transitive dependencies and the base distribution still require artifact review;
these copies alone do not close that release gate.

The Controller includes a modified Docker Compose v5.6.1 build, identified as
`v5.6.1-nsc.1`. NSC upgrades `golang.org/x/net` to v0.60.0 to address
CVE-2026-78669, with the required `x/crypto` v0.57.0 and `x/text` v0.42.0
dependencies. The upstream source archive is checksum-pinned in
[the image lock](build/image-lock.json); the exact module changes are recorded in
[the dependency patch](build/compose-dependencies.patch). Docker CLI remains the
unmodified binary from its pinned upstream image.
