# Code conventions

Follow the existing architecture and local conventions. Keep changes scoped and avoid unrelated formatting churn. Prefer small functions with explicit inputs, errors and state transitions over hidden global behavior.

## Go

Use gofmt and go vet. Propagate errors with useful context; do not swallow errors or treat an unknown result as success. Bound external I/O with contexts/timeouts, close resources and make retries idempotent and finite. Keep runtime/storage dependencies replaceable by isolated test fixtures. Preserve API-to-core boundaries and ownership checks in the execution layer, not only the UI.

## Vue and TypeScript

Use existing strict TypeScript and ESLint configuration, with zero lint warnings. Do not bypass types with explicit any, broad casts or disabled rules. Use Vue Query for server state, Pinia for preferences and the shared client for ETag, CSRF and idempotency behavior. Keep secrets and editable deployment drafts out of browser persistence.

ESLint is the authority for Vue template layout; Prettier options apply to script formatting. Do not run whole-file Vue Prettier rewrites that conflict with ESLint template rules. Verify lint after formatting. Use the existing PrimeVue components, accessibility conventions and theme tokens; read applicable library guidance before changing library integration.

## Contracts and dependencies

Edit OpenAPI/protobuf sources and regenerate their outputs; verify drift instead of manually patching generated code. Generated i18n follows the same rule. Keep pinned toolchain/image references consistent and review dependency changes separately from cosmetic edits. Do not introduce shell interpolation for untrusted data or log credentials. Follow [testing](testing.md) and [i18n](i18n.md) for every affected behavior.
