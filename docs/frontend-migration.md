# Vue console migration

> Historical provenance: original scaffold facts and six-locale counts are retained. Current supported locales are defined in the repository's internal/i18n/locales.json.

The Project Forge 0.3.0 frontend was generated in a temporary directory, validated,
and moved into `frontend/`. See `frontend/PROVENANCE.md` for the exact generation command.
The Controller now embeds Vite output instead of serving HTMX business templates.

## Compatibility

- `/login`, `/ui` and the ten existing section URLs remain available. Legacy login,
  logout and locale form endpoints remain supported; business `/ui` POST routes are retired.
- `/api/v1` keeps the Bearer contract, cursor pagination, ETags, idempotency and RFC 9457
  errors. It additionally accepts authenticated Cookie sessions with CSRF headers.
- Session bootstrap, JSON login/logout and locale selection use the documented
  `/api/v1/auth/*` and `/api/v1/locale` endpoints. No administrator token is stored in
  browser storage. Invalid explicit Authorization never falls back to a Cookie on business APIs.
- All six existing locales are retained; the first request follows browser/server
  negotiation and subsequent choices use the HttpOnly locale Cookie. UI dictionaries
  and PrimeVue labels update without replacing form drafts.
- The production deployment remains a single Controller container or binary. Node is
  required only for building assets. Database schemas and Gateway deployment are unchanged.

## Interaction changes

Lists use cursor pagination, loading/error states and periodic refresh. Forms retain
local drafts while data refreshes. Resource editing uses the revision captured when the
editor opens. Mutations never retry automatically. Application install and revision
flows present a redacted preflight and differences before queuing changes; operation
status is polled until a terminal state. Managed/external service boundaries remain enforced.

## Validation

- Generated scaffold: complete harness including Compose configuration checks.
- Frontend: lint, typecheck, generated API drift, six-language keys/placeholders,
  production build and 10 unit tests.
- Browser: five Playwright scenarios against a freshly built Controller container.
  Actual HTTP flows cover authentication, Pool/Runtime creation, editing, all sections,
  locale negotiation and persistence, draft preservation, CSP, desktop/mobile navigation
  and both themes. Application/binding/certificate UI workflow tests use deterministic
  intercepted responses after real Runtime creation.
- Backend integration: the existing real Docker management suite passed preflight,
  idempotency, raw/key environment edits, DNS/domain binding, redeploy, restart, scale,
  stop/start, down/start and failed-deployment rollback.
- Go: full package tests and vet; API/i18n race checks run in the existing Go/GCC image.
- Packaging: Controller Docker build and every existing Compose profile validate.

Reproduce with `make frontend-check`, `make test`, `make test-e2e-ui` and
`make test-e2e-management`. On a fresh checkout, run `make frontend-build` before invoking
Go directly for packages that import the API. Build outputs and test artifacts are ignored.
