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

## Component library (shadcn/ui)

The UI uses [shadcn/ui](https://ui.shadcn.com) primitives, vendored as source
into `src/components/ui/` via `npx shadcn@latest add <name>` (config:
`components.json`). The design tokens in `src/index.css` are the shadcn HSL
variable set exposed through Tailwind v4's `@theme inline`; keep them HSL — do
not let `shadcn init`/`add` rewrite the file to oklch (diff `src/index.css`
after any `add` and revert token churn). Vendored components import the scoped
`@radix-ui/react-*` packages, not the unified `radix-ui` meta-package.

### Surfaces

Three surfaces have to stay distinguishable from each other in both modes, and
which token a component reaches for follows from which of the three it is:

| Surface | Token | What sits on it |
| --- | --- | --- |
| Page | `--background` | `body` and the `AppShell` `<main>`; nothing else fills with it |
| Raised sheet | `--card` / `--popover` | Cards, dialogs, drawers, menus, the header and sidebar, the active face of a tab track |
| Recessed fill | `--muted` (and `--secondary` / `--accent`) | Tab tracks, code and `pre` blocks, table header rows, hover states |

Light runs muted &lt; background &lt; card, dark runs background &lt; card &lt;
muted, so `--background` and `--card` are never interchangeable even though
stock shadcn ships them equal. Four rules follow.

- **`--background` is the page and nothing else.** As an opaque fill it belongs
  only on `body` and `<main>`. Inside a card it was invisible while the two
  tokens were equal; now it reads as a tinted well in light and a sunken black
  box in dark. (A translucent `bg-background/40`-style scrim over an image or a
  panel is not a fill and is unaffected.)
- **A control or nested sheet takes `--card`**: outline buttons, list rows,
  modal surfaces, the active face of a tab track. **A well takes `--muted`**:
  code and `pre` blocks, preview frames, scroll wells, tab tracks themselves.
- **A field takes `bg-transparent`** (what `ui/input` and `ui/textarea` do) so
  it inherits whatever surface it lands on. Add `dark:bg-input/30` alongside it
  on a hand-rolled field to match the primitives.
- **A fill that must match its container names the container's own token.**
  `CollapsibleMarkdown`'s `fadeFrom` is `from-card` inside a card and
  `from-background` on the page; naming the wrong one shows a seam.

Two consumers cannot read the tokens at all and have to be updated by hand when
the palette moves: `ThumbnailGenerator`'s `DARK_SCHEME`/`LIGHT_SCHEME` (html2canvas
cannot resolve custom properties, and the thumbnail is stored as a blob, so a
stale value is permanent) and the public viewer's inline
`internal/portal/publicviewer/templates/public_viewer.html` token block.

Where a component needs the surface *furthest* from `--muted` rather than a
named one — the image viewer's checkerboard, a switch knob on a muted track —
that is `--card` in light and `--background` in dark, so it needs both.

App-level composition patterns live in `src/components/patterns/`:

| Component | Contract |
| --- | --- |
| `PageHeader` | The one shape a detail view opens with: back link, breadcrumb, icon + title, mono `urn` line, right-aligned page actions |
| `SectionCard` | The one way a page section is boxed; the section's action lives in its header |
| `EmptyState` | The only permitted use of a dashed border: "there is nothing here", with an optional action |
| `InfoHint` | A view's explainer prose behind an info toggle instead of an always-on paragraph |
| `FilterSelect` | One facet of a filter bar: a compact listbox named for assistive tech, whose "no filter" choice travels under a sentinel |
| `Pager` | The one page-through control: "Showing 1&ndash;20 of 500" on the left, Prev / page position / Next on the right |
| `SortableHead` | A `ui/table` header that sorts on click, with the direction shown on the sorted column |
| `SearchInput` | A list's free-text filter: a `ui/input` with the magnifier inside its leading edge |
| `SegmentedControl` | A small "which way do I want this shown" switch: adjacent faces in a bordered trough, the chosen one filled |
| `DrawerShell` | The one shape a right-hand detail slide-over takes: dimmed page, title and close along the top, the detail scrolling under it, an optional pinned footer for the action it exists to offer; Escape closes it |

Conventions the exemplar (Knowledge > Catalog) established: buttons, inputs,
labels, textareas, selects, tables, tabs, badges, and alerts come from
`src/components/ui/` — no inline Tailwind button/input recipes in new code.
Status pills wrap `ui/badge` semantic variants (`success`/`warning`/`danger`/
`info`/`muted`, e.g. `components/cards/StatusBadge.tsx`) rather than restating
tint classes. Warnings, errors, and success notices are `Alert`, whose
`destructive`/`warning`/`success` variants carry the same tints as the matching
badge variants; dashed boxes are reserved for `EmptyState`.

A modal is a `ui/dialog` whenever it wants a focus trap, Escape handling and
ARIA wiring — which is every dialog the app opens over a page. The overlays
that predate Radix keep `components/ModalShell.tsx`, and both routes take their
geometry from `lib/modal`, so a modal cannot be half-fixed: the overlay is the
scroll container and `DialogContent` renders *inside* it, because the stock
shadcn content centers itself with a translate and a dialog taller than the
viewport then loses its own title bar off the top edge with no way to scroll
back. `DialogContent` comes in two shapes — the default keeps its natural
height and lets the backdrop scroll, and `capped` bounds the panel at the
viewport and lays it out as a column so a header stays put while the body
scrolls (`HelpDialog`). A capped panel carries no padding of its own: each
region pads itself, or the header scrolls away with the body it heads.

`showCloseButton={false}` is not decoration: `ConfirmDialog` and `PromptDialog`
refuse Escape and outside clicks while a mutation is in flight, and Radix's
corner Close would be the one exit those guards do not cover.

The saved-things surfaces (Assets, Collections, the collection viewer) share
three more app-level pieces alongside those patterns: `cards/ThumbCard` is the
clickable gallery tile (thumbnail or stand-in icon, then whatever the list says
about the item), `ContentTypeBadge` names what an asset is and carries
`contentTypeIcon` for the matching glyph, and `ShareIndicators` is the
users/public-link pair. `components/listView.ts` holds the grid-or-table
preference the two lists share — the layout half of what `ScopeFilter` does for
ownership — plus the Load-more derivation both browse hooks use.

The knowledge surfaces share three app-level pieces of their own:
`knowledge/KnowledgeStatusBadge` owns the lifecycle vocabulary (pending,
approved, applied, rejected, rolled back, superseded, active, stale, archived)
and the tint each word carries, so a status is the same colour in the review
queue, a reader's own lists, a changeset row, and a page's lineage;
`knowledge/UrnBadge` names a linked catalog entity in a dense list (readable
tail, whole URN on hover) and is deliberately not an `EntityChip`, since nothing
it renders is navigable; `knowledge/EntityChip` stays the navigable citation and
now rides `ui/badge`, square-cornered so it reads as a citation rather than a
status pill. Approved is `info`, not `success`: an approved insight is cleared
to be applied, not yet applied, and only `applied` is the finished state.

`SegmentedControl` is a group of toggle buttons (`role="group"`, `aria-pressed`),
not a tablist: nothing under it is a tab panel, the same content is redrawn. So
its faces stay `getByRole("button", ...)`, unlike `ui/tabs` and unlike
`ScopeFilter`, whose Mine/Shared/All faces really are `role="tab"`. It also
replaces the hand-rolled `role="radiogroup"` switches the knowledge surfaces
used to carry (cards-or-graph, explore-or-whole-corpus, the graph's hop radius),
so a spec that drove those by `getByRole("radio", ...)` now asks for a button —
and, where the face's name is a bare number, scopes the query to the group by
its label.

`Card` takes `asChild`, like `Badge`: a card whose box is a landmark keeps the
landmark and takes the card face, e.g. `<Card asChild><aside>…</aside></Card>`
for the knowledge graph's inspector, which Playwright reaches as
`getByRole("complementary")`. Restating the face as loose classes on the
semantic tag is the thing this avoids.

A shadcn `Select` is a Radix listbox, not a native `<select>`: an item cannot
carry an empty value, so a facet's "no filter" choice travels under a sentinel
that the component translates back at its own boundary, and Playwright chooses
an option by clicking the trigger then the option rather than `selectOption()`.
In jsdom there is no `PointerEvent` at all, so the trigger's pointerdown
handler never runs and `fireEvent.pointerDown` cannot open it: a unit test
opens the listbox with `fireEvent.keyDown(trigger, { key: "Enter" })` and then
clicks the option (see `pages/audit/tabs/NotificationsTab.test.tsx`). The setup
file stubs the pointer-capture and `scrollIntoView` calls Radix makes while a
menu is open.

`ui/tabs` triggers are tabs, not buttons: a bar converted from hand-rolled
buttons breaks every `getByRole("button", { name: "<tab>" })` in the specs that
drive it. The dashboard's primary bar uses the `line` variant, which needs
`group-data-[orientation=horizontal]/tabs:h-auto` on the list (plain `h-auto`
loses to the variant's compound selector) and, when the list carries the
bar's own `border-b`, `after:bottom-[-1px]` on the trigger so the active
underline lands on that border instead of below it. A Radix trigger selects on
`mousedown`, not `click`, so a jsdom spec drives it with
`fireEvent.mouseDown` — `fireEvent.click` leaves the bar on its old tab and the
assertion that follows fails somewhere else entirely. Playwright's `click()` is
a real mouse press and needs no such accommodation.

`MarkdownEditor` sizes itself to its parent (`h-full`), so it must not be a
stretched grid item: in a grid cell it renders as tall as the cell *plus* its
own label and spills over the fields below. Give it a plain block parent.

`ui/table` puts `whitespace-nowrap` on every cell. A table with more than about
four columns of prose therefore overflows its card and hides the trailing
column behind a horizontal scroll: opt the prose cells back in with
`whitespace-normal` (see `pages/settings/keys/KeysTable.tsx`) rather than
letting the actions column disappear. `ui/textarea` has the mirror-image trap —
`field-sizing-content` silently overrides a stated `rows`, so add
`field-sizing-fixed` whenever the caller asks for a fixed height.

`PageHeader` lays its title block and actions out in one `flex-wrap` row, and
flex wraps *before* it shrinks: a subtitle long enough to fill the row pushes
the page action onto its own line. Give long header prose its own measure
(`<span className="block max-w-2xl">`) so the action stays on the title's row.

A wide table whose columns are stated as percentages needs `table-fixed` on the
`ui/table` as well, because the `whitespace-nowrap` above means auto layout
gives every column its full intrinsic width and starves the one column meant to
truncate (`pages/resources/parts/ResourcesTable.tsx` is the worked example).
Fixed layout then makes the percentages binding, so a set that sums past 100
spills each column's content into the next: state the widths as one list per
table shape and keep each list at 100.

A shadcn `Select` is a Radix listbox, not a form control, so it contributes
nothing to `FormData`. A dynamic form that reads its values back out of the DOM
— `pages/tools/ToolForm.tsx`, which renders whatever a tool's input schema
declares — pairs each listbox with a hidden input carrying the chosen value.
`required` then has to be enforced in the submit handler rather than by the
browser, since a hidden field cannot raise the native validation bubble.

The app shell states its own rail vocabulary in `components/layout/sidebar/`:
`NavButton` is the one face a rail row wears (a section link, the collapse
toggle, Sign Out), so a row cannot look like a nav item in one part of the rail
and like something else in another; `NavSection` is a captioned run of them;
`navItems` holds both item lists and `isNavActive`, which decides which single
item is lit for a route — including the routes a section owns but does not
name (Collections and the viewers under Assets, `/knowledge/pages/:id` under
Knowledge). A collapsed rail has no room for a waiting-work count, so
`NavButton` shows a dot carrying the count's words instead: the cue survives
the collapse even though the figure does not.

The feedback surfaces share `components/feedback/ThreadBadges`, which owns both
feedback vocabularies — the seven kinds a thread can be opened as, and the five
states it can be in — so a thread is the same colour in the activity feed, the
per-item drawer, the worklist, and its own header. Answered is `secondary`, not
`success`: an answered thread has had a reply, not a resolution, and only
`resolved` is the finished state. The resource library states its two
vocabularies the same way in `pages/resources/parts/badges.tsx`, where a
deployment's own category falls back to `muted` rather than borrowing a
built-in category's meaning.

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
| `max-lines` | 600 | **error** | `package_budget_test.go` |
| `sonarjs/no-identical-functions`, `sonarjs/no-collapsible-if` | — | warn | — |

Generated API types (`src/api/generated/`), MSW mocks (`src/mocks/`), and test
files (`*.test.{ts,tsx}`) are exempt from the complexity and size budgets — they
are not hand-maintained component source.

**Error-level rules fail the build; warning-level rules do not.** `npm run lint`
exits non-zero only on error-level violations, so the remaining warning-level
proxies (`max-lines-per-function`, `max-params`, `import-x/max-dependencies`)
flag regrowth for a follow-up split without blocking a PR.

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

Separately, the 15 files that exceeded the 600-line `max-lines` backstop were
decomposed in #819, and **`max-lines` is now error-level** — no non-generated
source exceeds 600 lines, so any file that crosses the budget fails `npm run
lint`. Each was split by responsibility into a facet directory per the #766
pattern (see `src/pages/settings/catalogs/`, `src/pages/audit/tabs/`, and
`src/pages/settings/connections/` for examples).
