---
version: alpha
spec: https://github.com/google-labs-code/design.md
name: mcp-data-platform-docs
description: Local design adoption record for the mcp-data-platform.txn2.com documentation site. References txn2/www DESIGN.md as the canonical visual identity for tokens, typography, components, voice, and accessibility rules. Records only the decisions and MkDocs Material learnings that the canonical does not cover.
upstream:
  design: https://github.com/txn2/www/blob/master/DESIGN.md
  tokens: https://github.com/txn2/www/blob/master/tokens.json
  sister: https://github.com/txn2/mcp-datahub/blob/main/DESIGN.md
adoption: token-alignment
stack:
  generator: MkDocs
  theme: Material for MkDocs
  templates: docs/overrides/
  styles: docs/stylesheets/extra.css
---

## What is canonical

The canonical visual identity for txn2 lives in [`txn2/www/DESIGN.md`](https://github.com/txn2/www/blob/master/DESIGN.md) with tokens in [`txn2/www/tokens.json`](https://github.com/txn2/www/blob/master/tokens.json). This file defers to those for everything below. If a value here disagrees with upstream, upstream wins.

mcp-data-platform mirrors the implementation pattern established by sister projects [`txn2/mcp-datahub`](https://github.com/txn2/mcp-datahub/blob/main/DESIGN.md), [`txn2/mcp-s3`](https://github.com/txn2/mcp-s3/blob/main/DESIGN.md), and [`txn2/mcp-trino`](https://github.com/txn2/mcp-trino/blob/main/DESIGN.md). When in doubt, copy mcp-datahub verbatim and edit only the project-specific content (symbol, hero copy, stack list, llms.txt, OG card text).

| Concern              | Source of truth |
|----------------------|-----------------|
| Color palette        | upstream `tokens.json` `color.*` |
| Typography stack     | upstream `tokens.json` `font.*` |
| Type scale           | upstream `DESIGN.md` Typography table |
| Spacing / measure    | upstream `tokens.json` `size.*` |
| Component contracts  | upstream `DESIGN.md` Components |
| Voice / copy rules   | upstream `DESIGN.md` Voice and Copy |
| Accessibility rules  | upstream `DESIGN.md` Do's and Don'ts |
| Mermaid theme        | upstream `DESIGN.md` `mcp__card--feature` block |
| MkDocs Material file map | sister `mcp-datahub/DESIGN.md` File map |

Tokens are mirrored as CSS custom properties in `docs/stylesheets/extra.css` `:root`. They are duplicated for runtime use, not as a divergence point. When upstream changes a token, update the value in `extra.css` and ship.

## Adoption level: token alignment

Per the upstream downstream contract, three levels are valid:

1. Reference. Link to upstream, no visual changes.
2. Token alignment. Keep MkDocs Material, re-skin via `extra.css` against upstream tokens.
3. Full re-skin. Replace MkDocs Material with custom layouts.

mcp-data-platform runs at **level 2**, matching its sister projects mcp-datahub, mcp-s3, mcp-trino, kubefwd, and txeh. The site keeps Material's instant nav, search, sidebar, code copy, and content extensions. The visual layer is replaced. The homepage is a custom Material template that takes over `block header`, `block container`, and `block footer` for full-bleed treatment.

## File map

| Path | Role |
|------|------|
| `mkdocs.yml`                              | Single dark `slate` palette. `font: false` so CSS loads the upstream Google Fonts URL with trimmed axes. |
| `docs/index.md`                           | Stub front matter with `template: home.html`. All homepage HTML lives in the template. |
| `docs/overrides/main.html`                | Adds the upstream Google Fonts `<link>`, full SEO surface (OG, Twitter, JSON-LD `SoftwareApplication`), the canonical/author meta tags per the upstream "SEO and social cards" spec, and includes `mermaid-fullscreen.js` before Material's bundle. Inherited by every page. |
| `docs/images/mcp-data-platform-og.svg`    | Source for the 1200x630 social card. Edit this, then re-rasterise. |
| `docs/images/mcp-data-platform-og.png`    | Rendered OG card. Linked from `og:image` and `twitter:image`. Re-render with `rsvg-convert -w 1200 -h 630 -o docs/images/mcp-data-platform-og.png docs/images/mcp-data-platform-og.svg`. |
| `docs/images/mcp-data-platform-symbol.svg`| Square geometric mark used by the hero. Two paths only: paper-toned three-node constellation + signal-orange platform hub. |
| `docs/llms.txt`                           | LLM-friendly docs map per the upstream `llms.txt` spec. Update when new top-level docs pages ship. |
| `docs/overrides/home.html`                | Custom homepage template. Overrides `block header` (rail), `block tabs` (empty), `block container` (page--home shell with hero, sections, flagship cards, stack, coda), `block footer` (home-footer). |
| `docs/overrides/404.html`                 | Restyled not-found page. Inherits `main.html`, uses `.md-typeset` body so the rail and footer match. |
| `docs/stylesheets/extra.css`              | All design rules. Two halves: homepage components scoped under `.page--home`, and Material chrome restyle for inner pages via `[data-md-color-scheme="slate"]` variable overrides. Also re-skins the existing `.mermaid-fullscreen-overlay` viewer against txn2 tokens. |

## Project-specific components

Components ported from upstream (via mcp-datahub) verbatim, with mcp-data-platform content:

- `.rail` (replaces Material `.md-header` on the homepage). Brand links to `./`. Live UTC clock in meta. txn2.com link in meta as `part of <em class="serif">txn2</em> ↗`.
- `.hero__main` (project-site hero variant): square symbol on the left, three-row Fraunces display on the right. mcp-data-platform / semantic platform / for ai. See upstream `.hero__main` and `.hero__mark` component spec.
- `.hero__mark` linking to `https://github.com/txn2/mcp-data-platform`. Symbol file at `docs/images/mcp-data-platform-symbol.svg` (square, viewBox `10 10 80 80`, two paths: three paper-toned outer nodes plus thin connecting bars combined into one base path, signal-orange platform hub disc accent). Accent breathes per upstream spec.
- `.section`, `.section__index`, `.section__title`.
- `.flagship__card`. Two cards: a server card (standalone platform install + claude mcp add demo) and a library card (Go composition with `platform.New`, `WithConfigFile`, `WithToolkit`, `Use`, `Run`). Top accent line animates on hover per upstream spec.
- `.terminal`, `.terminal__bar`, `.terminal__body` with `.t-prompt`, `.t-ok`, `.t-mute` classes. The only block with shadow.
- `.stack`, `.stack__row`. Each row links to a real anchor in the docs (cross-enrichment/, server/, auth/, personas/, knowledge/, ecosystem/).
- `.coda` and `.home-footer` (renamed from upstream `.footer` to avoid collision with markdown that uses `class="footer"`; see kubefwd Learning #5).

Components from upstream **not used** here, with reason:

- The 5-column footer's `sponsors / craig` columns. The mcp-data-platform home-footer has `about / docs / interfaces / code / txn2 / org` columns instead, since this site is project-scoped.

Custom additions specific to mcp-data-platform:

- The hero subtitle reads "semantic platform" rather than "metadata catalog" (mcp-datahub), "object storage" (mcp-s3), or "federated sql" (mcp-trino) to position mcp-data-platform as the orchestration layer that composes the sister servers and adds the operational layer (auth, personas, audit, gateway, portal, knowledge, memory). The upstream design refers to this site as "the catalog" of MCP servers; in practice it is broader than a catalog, so the tagline foregrounds the differentiating semantic-first cross-enrichment.
- The server flagship card's terminal demo uses `go install` + `claude mcp add data-platform` invocation against a YAML config file. The library card shows the canonical `platform.New` / `WithConfigFile` / `WithToolkit` / `Use` / `Run` path, which mirrors the project's actual Go API surface.
- The stack list emphasises the eight defining capabilities of this project beyond a single MCP server: cross-enrichment, composability, OAuth 2.1 inbound and outbound, personas, audit, gateway, portal, and knowledge/memory. This is wider than the sister projects' stack lists because mcp-data-platform is the platform, not a single toolkit.
- An `ecosystem` callout in the stack list points at the sister MCP projects (`mcp-datahub`, `mcp-s3`, `mcp-trino`) so readers see the broader composable suite. Links go to each project's documentation site, not its GitHub repo.
- The site preserves the existing `docs/javascripts/mermaid-fullscreen.js` viewer (loaded from `main.html`) and re-skins its overlay, toolbar, and expand button to txn2 tokens.

## Symbol design

`docs/images/mcp-data-platform-symbol.svg` is a custom geometric mark in viewBox `10 10 80 80` with exactly two paths:

- **Base** (filled `var(--paper)`): three small circles arranged in a triangle (top at (50,22), bottom-left at (22,72), bottom-right at (78,72), each radius 7.5) plus three thin connecting bars from each outer node toward the central platform hub. Combined into one path via concatenated sub-paths.
- **Accent** (filled `var(--signal)`): the central platform disc (cx=50, cy=52, radius 12), the largest single shape and the visual focal point. Breathes per the upstream `mark-breath` keyframes.

The visual reads as composition: three data sources (DataHub, Trino, S3) feeding into a central platform hub. The geometry mirrors the existing brand's "triangle network" lockup (`docs/images/mcp_data_platform_logo.svg`) reduced to its essential form and recoloured to the txn2 palette. The accent breathing pulse animates only the central disc, leaving the outer nodes and connecting bars static.

## MkDocs Material learnings

The Material learnings list applies to every MkDocs Material project re-skinned to the txn2 identity. mcp-data-platform inherits them all from kubefwd, mcp-datahub, mcp-s3, and mcp-trino. Read the full set in [`txn2/kubefwd/DESIGN.md`](https://github.com/txn2/kubefwd/blob/master/DESIGN.md) "MkDocs Material learnings". Brief summary so this file remains useful in isolation:

1. Override the homepage via a separate template, not via CSS hacks.
2. Re-skin inner pages via Material variable overrides on `[data-md-color-scheme="slate"]`.
3. `font: false` to load fonts directly from CSS.
4. Scope every homepage component class under `.page--home`.
5. Rename `.footer` to `.home-footer` to avoid collision.
6. `h3` and `h4` are technical reference, not display type. Switch them to Instrument Sans bold; flip any heading containing inline code to JetBrains Mono via `:has(code)` with a `@supports not selector(:has(*))` fallback.
7. Tabbed content nests boxes by default. Strip `.tabbed-set` background and border, keep only the label underline.
8. Mermaid via Material's `--md-mermaid-*` CSS variables, not via separate init.
9. Guard inline scripts against `navigation.instant` rehydration. The live UTC clock uses a `window.__mcpDataPlatformClock` sentinel.
10. Drop the light/dark toggle. Single `scheme: slate`.
11. Atmospheric overlays at low z-index (grain and vignette at z-index 1, below rail at z-50).
12. Hugo-only token compilation does not exist in MkDocs. Token sync is a manual edit to `extra.css`.
13. Image headings (`# ![alt](banner.svg)`) used by some inner pages need `.md-typeset h1:has(img)` margin/padding zeroing so the banner sits flush.

## Voice and copy

Defers to upstream. Briefly:

- No em-dashes (U+2014) or en-dashes (U+2013) anywhere, including code comments and template comments. Use commas, periods, colons, parentheses, slashes, hyphens.
- No AI-tell vocabulary: `seamless`, `leverage`, `comprehensive`, `robust`, `delve`, `unleash`, `elevate`, `embark`, `tapestry`, `not just X but Y`, `as an AI`, `let me X`.
- Sentence case for body. Lowercase for rail and label text. Title case rare.
- Section indices: `§ 01 / title` with slash, never an em-dash.
- Year ranges use a hyphen: `2025-2026`.
- Verify before commit: `grep -RE "—|–" docs/overrides/ docs/stylesheets/ docs/llms.txt mkdocs.yml DESIGN.md`. The published markdown under `docs/` may pre-date this rule; treat as advisory rather than blocking when sweeping prior content.

## Updating

When the upstream `txn2/www/DESIGN.md` or `tokens.json` changes:

1. Read the upstream diff. Identify which tokens, components, or rules changed.
2. Update the matching CSS variables in `docs/stylesheets/extra.css` `:root`.
3. If a component contract changed (padding, border, hover behavior), update the homepage template in `docs/overrides/home.html` and the matching CSS rules.
4. Update this file's File map / Project-specific components sections if a new component is added or removed.
5. Run `mkdocs build --strict` and verify in the browser before committing. Verify the home page hero, flagship cards, terminal, stack, coda, and home-footer. Verify an inner page (`/server/overview/`, `/library/overview/`, `/cross-enrichment/overview/`) still inherits the look.

Keep this file thin. If a section grows past 30 lines, ask whether it belongs upstream instead.
