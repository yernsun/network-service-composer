# Frontend restoration provenance

## Authoritative input

- File: repository-root `nginx-service-composer.tar.gz` (retained unchanged).
- Size: 25,336,826 bytes.
- SHA-256: `4e848bbe5ee4e643481906ca05bddcbf54251a853ba5ee913a2290fb414b0d55`.
- Contents used: 457 presentation templates, six renderers, original workspace
  controller, router, styles, icons, modal and toast components, and compiler.
- Dependency directories, bundles and historical acceptance outputs were not imported.

The previous frontend was backed up under
`coverage/frontend-restoration/20260915T044159Z/` before rebuilding its source and
tests. Its custom navigation, aggregate tabs and reused legacy feature editors
are not part of the restored workspace. The branch remains `dev-0.2.0`; the product
version is unchanged.

## Permitted adaptation

`reference/workspace.js`, `reference/demo.js`, `reference/compiler.js`,
`reference/router.ts` and `reference/views` preserve the supplied source. Generated
runtime differences isolate data access and simulation. Navigation and editor
interaction functions retain their original behavior for populated projects. The
explicit `scripts/project-scope.mjs` adaptation also permits legacy projects with
zero environments to open their overview, environment list, project variables and
empty aggregate lists. It uses the original leave guard, clears the previous
environment and continues to reject foreign or missing explicit environment IDs.
It neither creates a placeholder environment nor changes populated-project
environment/section switching. Both runtime modes use this same adaptation.
Explicit Vue I18n keys replace
text matching; resource names, form values, code and logs are not translated.
Open modal presentation is refreshed on language changes while preserving entered
values, acknowledgements, disabled state, focus and text selection. The original
modal actions and continuation callbacks are retained.
Authentication is outside the archive workspace. Unsupported operations use the
original submission error surface and preserve user input.

Service reference labels resolve the API service name by its unchanged ID in both
modes. The explicit manifest in `scripts/service-references.mjs` limits this
adaptation to presentation text (including the service mapping's accessible label),
change summaries and existing name/ID searches.
Missing names use a localized unavailable label containing the ID. Selector values,
links, action parameters, request payloads and technical ID fields retain IDs.
Generation tests reject identity attribute changes and unexpected reference-source drift.

`npm run ui:check` verifies archive SHA-256, original navigation/guard functions,
router integration, compiler equivalence and renderer equivalence after reversing
translations and applying the declared service-name changes, project card settings
extension and empty-project scope adaptation,
457 template definitions and the complete 174-action capability inventory.
Browser parity tests create their reference screenshots from the unchanged input
for each run. This integrity check is complementary to real API and browser tests.

## Translation provenance

The 2026-09-18 Compose validation change explicitly replaces the production
deployment import parser and adds a service placement workspace. The narrow
`scripts/compose-wizard.mjs` adaptation adds syntax and reviewed-deployment gates
to the production wizard button. Archive verification still checks the complete
function against that explicit transformation; demo behavior and original input
remain unchanged. The validation workspace uses the existing modal host and
adds its own unsaved-change handling. Behavior tests cover syntax diagnostics,
AST preservation, stale validation and required placement admission.

Existing project translations were retained where their meaning matches the
archive. Additional static phrases are extracted into
`reference/localization-source.json`; placeholders preserve interpolated data.
Draft translation used local Argos/CTranslate2 models and OpenCC. Model files and
translation runtime are development tools outside the repository and are not
production dependencies. No project strings were sent to the download hosts.
Machine-generated drafts require language review; structural key/placeholder
checks do not establish translation quality.

## Historical scaffold

The removed frontend originally came from Project Forge 0.3.0, template digest
`sha256:b500ef54df5fbbfb8daa010123aa5bda70d8d11fbb6b75de075b27a3e1e5d159`.
Its historical scaffold checks and later custom UI screenshots do not verify this
restoration. The existing Go repository is not managed by Project Forge.

## Approved project lifecycle extension

The project lifecycle iteration adds one settings button beside the original project
entry button. `scripts/project-lifecycle.mjs` asserts the exact source location and
unchanged identity attributes. `ProjectSettings.vue` uses the original modal and
form styles for metadata, deletion preview and actual task progress. Original
routing, environment switching and existing modal controllers remain authoritative.
Environment settings/empty deletion now call the implemented API through the
original steps. Real and demo lifecycle adapters are selected at build time; each
build rejects inclusion of the other execution adapter. The reference files and
archive are unchanged. No frontend source is excluded to improve coverage.

Project creation includes one runtime selector inside the existing modal, before
its initial-environment summary. `adaptProjectRuntime` wraps exactly template 219
without changing the 457 reference blueprints or the original controller steps.
It shows enabled, manageable runtime names and submits the selected ID as
`default_runtime_id`. Automatic selection retains the server default/unique-target
behavior; ambiguity focuses this field and keeps the existing inputs. The same
field is generated for demo mode, whose selected default remains in demo storage.
Archive parity hides only this added field when comparing the remaining original
modal geometry; creation E2E verifies the field visibly, including mobile layout.

## Protected built-in application display

The shared system-applications generator adaptation adds system ownership labels,
unknown-observation display and unavailable version markers. It preserves the
reference application layout, tabs and action order. Real inventory consumes the
API application environment reference independently of execution runtime and
does not request deployment source for system applications. Original references
and the input archive remain unchanged.

## Product rename adaptation

Network Service Composer for Nginx retains the original archive and reference
files above. `scripts/brand.mjs` explicitly replaces the display brand, About
heading and demo storage key when adapting those inputs. Archive checks apply
this same transformation before comparing current source; navigation, compiler,
renderers and templates remain subject to the existing equivalence checks.
