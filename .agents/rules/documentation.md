# Documentation policy

Write current repository documentation, AGENTS.md, rules and SKILL.md instructions in English. Root AGENTS.md explicitly links mandatory rules; rules are not assumed to be automatically loaded. Skills contain repository-specific procedures and link to policy instead of duplicating it.

Documentation classes are recorded in [documentation.json](documentation.json). Current maintainer/user documentation defaults to English. Locale directories retain their declared translations. Dated historical reports preserve original dates, versions, test counts and evidence; translation must not turn historical results into current claims. Legal text and raw provenance remain faithful to their source.

Legacy built-in installation READMEs are immutable artifact inputs. Their exact approved paths and SHA-256 values are recorded in the classification file; do not edit them or use this exception for new documents. Link current English readers to each application's docs/en/README.md. Preserve all translated market materials.

Validate local file links, skill metadata and classified paths. Review prose language and meaning manually: a non-ASCII ban cannot determine English quality and would damage translations and examples. Exceptions require an explicit reason; legal and historical exceptions are not a way to bypass code coverage.

Document actual supported behavior and limitations. Do not claim a requirement is implemented or a test passed without evidence. Update current language lists from the shared locale authority; preserve historical locale counts with a clear historical label.
