# mcp-data-platform UI

React + TypeScript + Vite admin portal for the MCP Data Platform.

## Scripts

```bash
cd ui
npm ci             # install pinned dependencies
npm run dev        # vite dev server (MSW-mocked API)
npm run typecheck  # tsc --noEmit
npm run lint       # ESLint, including the complexity/coupling gates below
npm test           # vitest unit tests
npm run build      # production build (tsc -b && vite build)
npm run test:e2e   # interactive Playwright suite (MSW-mocked)
```

All five of `typecheck`, `lint`, `test`, `build`, and the E2E suite run in the
`frontend` / `frontend-e2e` CI jobs (`.github/workflows/ci.yml`) and must pass
before merge.

## Frontend lint gates (#816)

The frontend enforces the same kind of per-function complexity budgets the Go
side enforces in `make verify`, plus a coupling rule. These are configured in
`eslint.config.js` and run in CI via `npm run lint`.

| Rule | Threshold | Level | Go analog |
| --- | --- | --- | --- |
| `complexity` | 10 | **error** | `gocyclo <= 10` |
| `sonarjs/cognitive-complexity` | 15 | **error** | `gocognit <= 15` (same algorithm) |
| `import-x/no-cycle` | maxDepth 4 | **error** | package layering invariants |
| `max-lines-per-function` | 250 | warn | — |
| `max-params` | 5 | warn | — |
| `import-x/max-dependencies` | 25 | warn | — |
| `max-lines` | 600 | warn | `package_budget_test.go` |
| `sonarjs/no-identical-functions`, `sonarjs/no-collapsible-if` | — | warn | — |

Generated API types (`src/api/generated/`), MSW mocks (`src/mocks/`), and test
files (`*.test.{ts,tsx}`) are exempt from the complexity and size budgets — they
are not hand-maintained component source.

**Error-level rules fail the build; warning-level rules do not.** `npm run lint`
exits non-zero only on error-level violations, so the size/fan-out proxies flag
regrowth for a follow-up split without blocking a PR.

### Ratchet baseline (`eslint-suppressions.json`)

The error-level rules are enforced against a committed
[ESLint bulk-suppressions](https://eslint.org/docs/latest/use/suppressions)
baseline, `eslint-suppressions.json`. This mirrors how the Go complexity budgets
were seeded: **existing** violations are recorded in the baseline and do not fail
CI, while any **new** violation (a new file over threshold, or an existing
baselined file growing an additional violation) fails `npm run lint`. Fixing a
baselined violation is always safe — a now-unused suppression prints an advisory
but does not fail the build.

When you intentionally split or simplify a legacy file, prune its stale entries:

```bash
cd ui
npm run lint -- --prune-suppressions   # drop suppressions no longer needed
```

Do **not** run `--suppress-all` to silence a genuinely new violation; that
defeats the gate. Add to the baseline only when deliberately accepting a
pre-existing offender you are not splitting in the current change.

### Triage list — existing offenders (for follow-up decomposition)

At the time this gate landed there were **111 error-level violations across 64
files** (100 `complexity`, 11 `sonarjs/cognitive-complexity`) and **0 import
cycles**. Per the #766 pattern, these are baselined rather than fixed in one
sweep; decompose them in follow-up PRs and prune the baseline as you go. The
complete machine-readable inventory is `eslint-suppressions.json`; the priority
targets (2+ violations in one file) are:

| File | `complexity` | `cognitive-complexity` | total |
| --- | --- | --- | --- |
| `src/pages/settings/CatalogsPanel.tsx` | 8 | 0 | 8 |
| `src/pages/assets/MyAssetsPage.tsx` | 4 | 1 | 5 |
| `src/pages/resources/ResourcesPage.tsx` | 3 | 2 | 5 |
| `src/components/layout/Sidebar.tsx` | 4 | 0 | 4 |
| `src/pages/collections/CollectionsPage.tsx` | 3 | 1 | 4 |
| `src/pages/knowledge-pages/KnowledgePagesPage.tsx` | 3 | 1 | 4 |
| `src/pages/knowledge/ContextDocsTab.tsx` | 3 | 0 | 3 |
| `src/pages/knowledge/KnowledgePage.tsx` | 3 | 0 | 3 |
| `src/pages/prompts/AdminPromptsPage.tsx` | 3 | 0 | 3 |
| 17 more files with 2 violations each | — | — | 2 |
| 38 files with 1 violation each | — | — | 1 |

Separately, 15 files still exceed the 600-line `max-lines` warning backstop
(largest: `src/pages/settings/CatalogsPanel.tsx` at 1578 lines). These are
warnings, not gate failures; split them by responsibility (see
`src/pages/settings/connections/` and `src/pages/settings/persona/` for the
decomposition pattern established in #766).
