# Archive interaction verification matrix

The supplied archive is the authority. Current execution results are stored with
each test run; this document defines coverage intent and does not assert PASS.

| Area | Original behavior to preserve | Evidence |
| --- | --- | --- |
| Project/environment navigation | Original top tabs and sidebar selector, section preservation and aggregate/detail fallbacks | `tests/restoration/parity.e2e.ts` |
| History and deep links | URL, selected item and data ownership agree after reload/back/forward | Parity and real workspace E2E |
| Dirty editors | Stay preserves input; discard follows original target route | Parity, unit and real variables E2E |
| Page and modal layout | Fresh archive screenshots with identical data, clock and viewport | 13 pages and 12 desktop/mobile modal comparisons |
| Variables | Original validation/import/review/save controls; real ETag conflict retains input | Compiler, workspace and adapter unit tests; real workspace E2E |
| Application install | Original four steps; real preflight identity and reviewed submission | Real executor E2E |
| Services/infrastructure | Explicit resource identity and real observations; unavailable evidence stays unavailable | Inventory unit tests and workspace browser checks |
| Domain and NGINX | Original form/file editors and actions; unsupported domain-local commit reports an error | Capability mapping and gap tests |
| Internationalization | Eight explicit catalogs; data and editor values remain literal | i18n checks and language browser tests |
| Demo isolation | Original simulation flows and distinct local state; zero business API requests | Demo E2E and build module-graph boundary |
| Authentication | Login/logout/expiry outside the original workspace; viewer mutations rejected | Transport tests and real workspace E2E |

Every original action appears in `reference/action-inventory.json` and
`reference/action-capabilities.json`. The detailed API evidence and missing server
behavior are maintained in `cookbooks/plans/frontend-backend-capability-todo.md`.
