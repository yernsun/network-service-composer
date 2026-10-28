---
name: nsc-i18n
description: Maintain NSC interface translations and localized marketplace README/manual revisions across the supported locales.
---

# nsc-i18n

Read [i18n](../../rules/i18n.md) and [documentation](../../rules/documentation.md). Obtain locales from internal/i18n/locales.json before editing catalogs or official manuals.

Change shared API text in internal/i18n/catalogs and UI-only text in frontend/src/shared/i18n/locales. Keep stable error codes separate from display text; regenerate frontend catalogs through the package scripts.

For marketplace changes, read docs/marketplace-manifest.md and follow its source-hash/translation acknowledgements. Never edit legacy immutable installation READMEs to update current documentation; use docs/en and the other locale directories. Review translations before advancing their acknowledgement hash.

Run catalog checks, relevant reader/backend tests and the official market build through make harness-check. Check missing-body and displayed-fallback behavior when document selection changes. Structural checks do not establish translation quality.
