# Release Highlights

A timeline of what is new in the Data Platform, newest first. Each entry is a
theme that shipped across a set of releases, with the dates it landed and links
to every release in that window. This focuses on capabilities and improvements
you can see and use; routine maintenance is left out of the summaries.

The [GitHub Releases](https://github.com/txn2/mcp-data-platform/releases) remain
the authoritative, per-release changelog and expand every highlight here into
full detail (exact commits, install instructions, and signed verification
artifacts).

If you are new here: the platform lets an AI assistant work with your data and
tools safely. It connects to data warehouses, a data catalog, file storage, and
external APIs, adds business context to answers, records every action, and lets
you save and share what the assistant produces.

---

## A first-class prompt library and relevance search everywhere
- Date Range: June 5 to 7, 2026
- Versions: [v1.79.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.79.2), [v1.80.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.80.0), [v1.81.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.81.0), [v1.81.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.81.1)

Prompts grew up and relevance search reached every corner of the platform. What
used to be a single-owner convenience became a governed, shareable library, and
free-text search that ranks by meaning now spans knowledge, prompts, saved
assets, and collections, in both the assistant and the portal.

- **A first-class prompt library.** Prompts gain a full lifecycle (draft, approved, deprecated), tags, admin promotion through a review queue, and direct user-to-user sharing by email, where the recipient gets a real, runnable prompt rather than a flattened copy.
- **Search by meaning, everywhere.** Relevance search (meaning plus keywords, with an automatic keyword-only fallback) now covers captured knowledge, prompts, saved assets, and collections, on both the assistant and the portal, always scoped to what you are allowed to see.
- **Consistent, self-describing errors.** Failed tool calls now report in a uniform, self-describing way, so the assistant can tell a fixable input mistake from an authentication problem or an outage.

## Self-service configuration and locked-down access
- Date Range: June 2 to 4, 2026
- Versions: [v1.77.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.77.0), [v1.77.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.77.1), [v1.77.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.77.2), [v1.78.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.78.0), [v1.79.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.79.0), [v1.79.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.79.1)

These releases rounded out reliability and control. The platform now handles
very large API responses and exports without strain, admins can configure the
whole system just by asking the assistant, and access control tightened so each
role gets exactly the connections it is granted and nothing more.

- **Reliable large responses and exports.** The API connector now handles very large and binary responses safely, streaming exports straight to storage so big results cannot exhaust the server.
- **Configure the platform by asking.** A built-in self-configuration connection lets an admin create roles, add connections, edit the assistant's instructions, and manage prompts and API keys through the assistant itself, with every change attributed and logged to that admin.
- **Tighter access control.** Connection access is now closed by default: each role gets exactly the connections it is granted and nothing more.
- **Readable connection notes.** Connections can carry formatted descriptions with a collapsible reveal.

## Visible intelligence and resilient operations
- Date Range: May 25 to 31, 2026
- Versions: [v1.67.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.67.1), [v1.67.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.67.2), [v1.67.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.67.3), [v1.67.4](https://github.com/txn2/mcp-data-platform/releases/tag/v1.67.4), [v1.67.5](https://github.com/txn2/mcp-data-platform/releases/tag/v1.67.5), [v1.68.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.68.0), [v1.69.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.69.0), [v1.69.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.69.1), [v1.70.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.70.0), [v1.70.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.70.1), [v1.71.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.71.0), [v1.72.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.72.0), [v1.73.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.73.0), [v1.74.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.74.0), [v1.75.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.75.0), [v1.75.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.75.1), [v1.75.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.75.2), [v1.76.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.76.0)

A busy window focused on making the platform's intelligence visible and its
operation resilient. New dashboards show the health of search and indexing, the
assistant can now find the right tool on its own, memory recall got smarter, and
several reliability improvements keep multiple copies of the platform in sync and
prevent bad settings from causing outages.

- **See the health of search and indexing.** A dashboard shows the state of the platform's semantic search across systems, with accurate coverage and clear status.
- **Find the right tool automatically.** The assistant can locate the best tool for a request by intent.
- **Smarter memory recall.** Memory search now combines meaning and keywords for better results.
- **More connectivity and reliability.** Support for file-based (WebDAV) services; activity logs split by source (assistant versus API); faster audit history through monthly partitioning; bad connection settings caught before they are saved; and configuration changes that propagate across multiple running copies of the platform.

## Operability, security, and more ways to authenticate
- Date Range: May 18 to 24, 2026
- Versions: [v1.62.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.62.3), [v1.62.4](https://github.com/txn2/mcp-data-platform/releases/tag/v1.62.4), [v1.63.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.63.0), [v1.64.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.64.0), [v1.64.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.64.1), [v1.65.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.65.0), [v1.66.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.66.0), [v1.67.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.67.0)

Operability and security took center stage. Connected APIs became usable from
outside the assistant, live operational dashboards turned on by default, more
authentication methods arrived for corporate and certificate-secured APIs, and a
redesigned role editor made permissions clearer. A security fix also closed a
cross-user visibility issue.

- **Use connected APIs from other tools.** A web endpoint exposes the API connector to non-assistant tools (for example data pipelines and scripts), under the same access rules and activity logging.
- **Operational dashboards, on by default.** Live health and usage metrics for the platform, with an admin metrics dashboard.
- **More ways to authenticate.** Support for basic username and password APIs, and for internal or corporate APIs that authenticate with certificates (mTLS).
- **Redesigned role editor.** A unified editor for a role's permissions and assistant-behavior settings.
- **Security fix.** Closed an issue that could let one user's activity history be visible to another on a specific connection type.

## The API connector matures
- Date Range: May 11 to 17, 2026
- Versions: [v1.59.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.59.0), [v1.60.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.60.0), [v1.60.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.60.1), [v1.61.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.0), [v1.61.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.1), [v1.61.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.2), [v1.61.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.3), [v1.61.4](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.4), [v1.61.5](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.5), [v1.61.6](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.6), [v1.61.7](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.7), [v1.61.8](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.8), [v1.61.9](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.9), [v1.61.10](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.10), [v1.61.11](https://github.com/txn2/mcp-data-platform/releases/tag/v1.61.11), [v1.62.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.62.0), [v1.62.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.62.1), [v1.62.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.62.2)

With the API connector in place, the next releases hardened it for real-world
use. APIs now have versioned catalogs the assistant can understand, sign-in
became more reliable with automatic refresh and a clear audit history, and
operation search began matching on intent rather than exact words.

- **The API connector matures.** Each API now has a versioned catalog so the assistant understands its operations and inputs, support for APIs that need a second credential header, and tolerance for the quirks of real-world API descriptions.
- **More reliable connection sign-in.** Sign-in for both AI-tool and web-API connections is unified, with automatic token refresh before expiry, a history of authentication events, and clearer errors.
- **Search by intent.** API operations are ranked by what you mean, not just keyword match, powered by a new background search-indexing system.

## Connect to any REST or HTTP API
- Date Range: May 6 to 8, 2026
- Versions: [v1.57.6](https://github.com/txn2/mcp-data-platform/releases/tag/v1.57.6), [v1.57.7](https://github.com/txn2/mcp-data-platform/releases/tag/v1.57.7), [v1.58.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.58.0), [v1.58.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.58.1), [v1.58.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.58.2), [v1.58.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.58.3)

One of the platform's biggest capabilities landed here: the ability to connect
to any external REST or web API. The assistant reads an API's own description to
learn what it can do, signs in securely, respects per-role limits on individual
operations, and can save the results as assets.

- **Connect to any REST or HTTP API.** A major capability: the assistant can now call external web APIs directly. It reads each API's published description to discover what operations are available, signs in securely on your behalf (OAuth), enforces per-role rules on individual endpoints, and can export API results as assets.

## Smoother external connections and a refreshed look
- Date Range: April 30 to May 1, 2026
- Versions: [v1.57.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.57.1), [v1.57.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.57.2), [v1.57.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.57.3), [v1.57.5](https://github.com/txn2/mcp-data-platform/releases/tag/v1.57.5)

A round of refinement made connecting external services less fiddly, with
clearer sign-in prompts, friendlier errors, and connections that activate
themselves once they are ready. The portal and documentation also adopted a
refreshed visual identity.

- **Easier external connections.** Connecting external services is smoother: a clear "Connect" button for sign-in, friendly error messages, and toolkits that turn themselves on once their prerequisites are met.
- **Refreshed look.** The documentation site and portal adopt a refreshed visual identity.

## Reaching out to other tool servers
- Date Range: April 23 to 26, 2026
- Versions: [v1.56.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.56.2), [v1.57.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.57.0)

The platform began reaching outward. A new gateway lets it connect to and use
tools hosted on other servers, weaving their capabilities and context into the
same experience, alongside a cleaner page for browsing everything available.

- **Connect to other AI tool servers.** A new gateway lets the platform connect to and use tools from other MCP servers, automatically sharing context across them.
- **Redesigned Tools page.** A cleaner master-detail layout for browsing tools.

## Exports and live updates
- Date Range: April 13 to 19, 2026
- Versions: [v1.55.7](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.7), [v1.55.8](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.8), [v1.55.9](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.9), [v1.55.10](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.10), [v1.55.11](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.11), [v1.56.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.56.0), [v1.56.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.56.1)

Getting results out of the platform and keeping clients in sync were the focus.
A new export turns any query straight into a downloadable file, and the
assistant's tool list now updates live for everyone connected whenever the
configuration changes.

- **Export results to a file.** A new export tool turns a query directly into a downloadable, shareable asset (CSV, JSON, or Markdown).
- **Live updates.** The assistant's available tools refresh automatically for connected clients when configuration changes.
- **Formatted knowledge and memory.** Knowledge and memory now render as formatted text in the portal.

## A longer memory and runtime flexibility
- Date Range: April 7 to 12, 2026
- Versions: [v1.50.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.50.2), [v1.50.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.50.3), [v1.51.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.51.1), [v1.52.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.52.0), [v1.53.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.53.0), [v1.54.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.54.0), [v1.55.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.0), [v1.55.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.1), [v1.55.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.2), [v1.55.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.3), [v1.55.4](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.4), [v1.55.5](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.5), [v1.55.6](https://github.com/txn2/mcp-data-platform/releases/tag/v1.55.6)

The assistant gained a longer memory and the platform gained more day-to-day
flexibility. Preferences and context now carry across sessions, prompts can be
created and edited on the fly, and people can upload their own reference files
for the assistant to use.

- **The assistant remembers.** A first-class memory layer keeps preferences, corrections, and context across sessions, for both agents and analysts.
- **Manage prompts at runtime.** Create and edit prompts through the portal, no redeploy needed.
- **Upload reference files.** Upload and manage files the assistant can use as reference material.

## Collections, editable settings, and per-role filtering
- Date Range: March 30 to April 4, 2026
- Versions: [v1.46.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.46.1), [v1.46.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.46.2), [v1.47.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.47.0), [v1.48.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.48.0), [v1.48.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.48.1), [v1.48.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.48.2), [v1.49.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.49.0), [v1.49.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.49.1), [v1.50.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.50.0), [v1.50.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.50.1)

This window made the platform more organized and more configurable without
engineering help. Saved work can be grouped into shareable collections, more
settings became editable directly in the admin screens, and what each role can
see and use is now filtered to fit.

- **Collections.** Group related assets into shareable collections with thumbnails and a browser.
- **Editable settings.** Granular configuration through an admin settings screen, with roles and API keys now stored in the database so they can be changed at runtime without a redeploy.
- **Per-role tool and data filtering.** Tools and connections are filtered by role.
- **Advanced catalog search.** Search the catalog with column-level filtering.

## Richer catalog edits
- Date Range: March 29, 2026
- Versions: [v1.46.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.46.0)

A small, focused release that broadened how the assistant can contribute to the
catalog, adding support for richer document-style changes when it writes context
back.

- **Richer catalog edits.** Support for "context document" change types when writing back to the catalog.

## Maintaining the catalog, not just reading it
- Date Range: March 16 to 21, 2026
- Versions: [v1.44.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.44.1), [v1.44.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.44.2), [v1.44.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.44.3), [v1.45.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.45.0)

Until now the assistant could read the data catalog; now it can help maintain
it. New tools let it create and update catalog entries, so documentation, tags,
and ownership can be improved through conversation rather than manual edits.

- **Update the catalog, not just read it.** The assistant gains tools to create and update catalog entries, so it can help maintain documentation, tags, and ownership.

## 1.0 and a polished sharing experience
- Date Range: March 9 to 15, 2026
- Versions: [v0.37.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.37.1), [v0.37.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.37.2), [v0.37.6](https://github.com/txn2/mcp-data-platform/releases/tag/v0.37.6), [v0.37.7](https://github.com/txn2/mcp-data-platform/releases/tag/v0.37.7), [v1.38.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.38.1), [v1.38.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.38.2), [v1.38.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.38.3), [v1.38.4](https://github.com/txn2/mcp-data-platform/releases/tag/v1.38.4), [v1.38.5](https://github.com/txn2/mcp-data-platform/releases/tag/v1.38.5), [v1.39.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.39.0), [v1.39.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.39.1), [v1.39.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.39.2), [v1.39.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.39.3), [v1.39.4](https://github.com/txn2/mcp-data-platform/releases/tag/v1.39.4), [v1.39.5](https://github.com/txn2/mcp-data-platform/releases/tag/v1.39.5), [v1.39.6](https://github.com/txn2/mcp-data-platform/releases/tag/v1.39.6), [v1.39.7](https://github.com/txn2/mcp-data-platform/releases/tag/v1.39.7), [v1.39.8](https://github.com/txn2/mcp-data-platform/releases/tag/v1.39.8), [v1.40.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.40.0), [v1.40.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.40.1), [v1.40.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.40.2), [v1.40.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.40.3), [v1.40.4](https://github.com/txn2/mcp-data-platform/releases/tag/v1.40.4), [v1.40.5](https://github.com/txn2/mcp-data-platform/releases/tag/v1.40.5), [v1.41.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.41.0), [v1.41.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.41.1), [v1.41.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.41.2), [v1.41.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.41.3), [v1.42.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.42.0), [v1.42.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.42.1), [v1.42.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.42.2), [v1.43.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.43.0), [v1.43.1](https://github.com/txn2/mcp-data-platform/releases/tag/v1.43.1), [v1.43.2](https://github.com/txn2/mcp-data-platform/releases/tag/v1.43.2), [v1.43.3](https://github.com/txn2/mcp-data-platform/releases/tag/v1.43.3), [v1.44.0](https://github.com/txn2/mcp-data-platform/releases/tag/v1.44.0)

The platform reached its **1.0 release** during this window, and the work
centered on making sharing genuinely pleasant to use. The public viewer matured
into a polished experience, and saved assets gained preview thumbnails and full
version history so nothing is ever lost.

- **A polished sharing experience.** A large investment in the public viewer: dark mode, expiration notices, sharing by email with permission levels, "save to my assets," view counts, and branded headers on shared links.
- **Preview thumbnails and version history.** Saved assets get automatic preview thumbnails and full version history, including the ability to revert to an earlier version.
- **One-click prompt workflows.** A reusable prompt system with categories and workflow prompts.
- **Spreadsheet exports.** Save and download results as CSV.

## The asset portal arrives
- Date Range: March 2 to 8, 2026
- Versions: [v0.31.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.31.0), [v0.32.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.32.0), [v0.33.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.33.0), [v0.33.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.33.1), [v0.33.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.33.2), [v0.33.3](https://github.com/txn2/mcp-data-platform/releases/tag/v0.33.3), [v0.33.4](https://github.com/txn2/mcp-data-platform/releases/tag/v0.33.4), [v0.33.5](https://github.com/txn2/mcp-data-platform/releases/tag/v0.33.5), [v0.35.6](https://github.com/txn2/mcp-data-platform/releases/tag/v0.35.6), [v0.35.7](https://github.com/txn2/mcp-data-platform/releases/tag/v0.35.7), [v0.35.8](https://github.com/txn2/mcp-data-platform/releases/tag/v0.35.8), [v0.35.9](https://github.com/txn2/mcp-data-platform/releases/tag/v0.35.9), [v0.35.10](https://github.com/txn2/mcp-data-platform/releases/tag/v0.35.10), [v0.36.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.36.0), [v0.36.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.36.1), [v0.36.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.36.2), [v0.37.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.37.0)

This is a milestone window. The asset portal arrived, turning the things the
assistant produces (dashboards, reports, charts) into saved, viewable, shareable
items rather than one-off outputs that disappear at the end of a chat. A single
unified portal with one sign-on brought it all together.

- **Save and share what the assistant creates.** The asset portal launches. Dashboards, reports, charts, and other outputs can be saved, viewed in the browser, and shared through a public, branded viewer, with a record of which tool calls produced each one.
- **One portal, one login.** A single unified portal with single sign-on.

## Tailored to who is asking
- Date Range: February 23 to March 1, 2026
- Versions: [v0.27.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.27.0), [v0.28.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.28.0), [v0.28.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.28.1), [v0.28.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.28.2), [v0.28.3](https://github.com/txn2/mcp-data-platform/releases/tag/v0.28.3), [v0.28.4](https://github.com/txn2/mcp-data-platform/releases/tag/v0.28.4), [v0.28.5](https://github.com/txn2/mcp-data-platform/releases/tag/v0.28.5), [v0.29.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.29.0), [v0.30.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.30.0)

The experience started tailoring itself to who is asking. The platform's
self-introduction now adapts to a person's role, tools carry friendly names, and
admins can publish their own reference material for the assistant to draw on.

- **Role-aware introduction.** The platform introduction adapts to each user's role, and tools now carry friendly, human-readable titles.
- **Custom reference material.** Admins can publish custom resources that the assistant can read on its own.

## Richer interactions, leaner answers
- Date Range: February 16 to 22, 2026
- Versions: [v0.20.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.20.0), [v0.21.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.21.0), [v0.21.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.21.1), [v0.22.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.22.0), [v0.22.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.22.1), [v0.22.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.22.2), [v0.22.3](https://github.com/txn2/mcp-data-platform/releases/tag/v0.22.3), [v0.23.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.23.0), [v0.23.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.23.1), [v0.23.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.23.2), [v0.24.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.24.0), [v0.25.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.25.0), [v0.26.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.26.0), [v0.26.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.26.1)

Two themes ran in parallel: making the assistant's conversational abilities
richer, and making its answers sharper. It gained reusable prompts, file
attachments, progress updates, and the ability to ask a clarifying question,
while its background context-gathering got more precise so responses stay
relevant without unnecessary noise.

- **Richer assistant interactions.** Support for reusable prompts, file and resource attachments, live progress updates, and the assistant asking a clarifying follow-up question when it needs one.
- **Smarter, leaner answers.** Context is now narrowed to the columns a query actually uses, empty descriptions are dropped, and search results include ready-to-run example queries and a schema preview, so answers are more relevant and quicker.
- **Admin portal polish.** Light and dark branding, version display, and the ability to replay a past tool call straight from the activity log.

## The admin dashboard and a knowledge loop
- Date Range: February 9 to 15, 2026
- Versions: [v0.15.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.15.0), [v0.15.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.15.1), [v0.15.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.15.2), [v0.15.3](https://github.com/txn2/mcp-data-platform/releases/tag/v0.15.3), [v0.16.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.16.0), [v0.16.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.16.1), [v0.16.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.16.2), [v0.17.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.17.0), [v0.17.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.17.1), [v0.17.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.17.2), [v0.18.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.18.0), [v0.18.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.18.1), [v0.18.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.18.2), [v0.18.3](https://github.com/txn2/mcp-data-platform/releases/tag/v0.18.3), [v0.18.4](https://github.com/txn2/mcp-data-platform/releases/tag/v0.18.4), [v0.19.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.19.0)

Administration moved from configuration files into a real web dashboard, and the
platform learned to get smarter over time. The standout addition was a knowledge
loop: the assistant can capture what people teach it during a conversation and,
with approval, fold that back into the shared catalog so the lesson sticks.

- **The admin dashboard arrives.** A web admin portal with an activity dashboard and the ability to run and inspect tool calls.
- **Capture and apply knowledge.** The assistant can capture insights from a conversation (for example, "that column is gross margin, not revenue") and, with admin approval, write them back into the data catalog, so no one has to explain the same thing twice.
- **Zero-downtime upgrades.** Active sessions survive restarts, so upgrades no longer interrupt people mid-task.
- **Control which tools are available.** Admins can turn specific tools on or off.

## Durable activity logging and smoother sign-in
- Date Range: February 4 to 8, 2026
- Versions: [v0.12.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.12.0), [v0.12.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.12.1), [v0.12.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.12.2), [v0.13.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.13.0), [v0.13.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.13.1), [v0.13.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.13.2), [v0.13.3](https://github.com/txn2/mcp-data-platform/releases/tag/v0.13.3), [v0.13.4](https://github.com/txn2/mcp-data-platform/releases/tag/v0.13.4), [v0.14.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.14.0)

This stretch was about trust and stability behind the scenes. The platform
gained complete, durable activity logging so every action is accountable, moved
its configuration onto a managed database, and smoothed out the sign-in
experience so logging in just works across different clients.

- **Complete activity logging.** Every action is now recorded with full detail, backed by a proper database with versioned migrations.
- **Smoother sign-in.** A round of fixes to the login flow (silent single sign-on, redirect handling) so signing in is seamless across clients.

## Lineage, orientation, and interactive results
- Date Range: January 27 to February 1, 2026
- Versions: [v0.8.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.8.0), [v0.8.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.8.1), [v0.8.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.8.2), [v0.9.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.9.0), [v0.9.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.9.1), [v0.9.2](https://github.com/txn2/mcp-data-platform/releases/tag/v0.9.2), [v0.9.3](https://github.com/txn2/mcp-data-platform/releases/tag/v0.9.3), [v0.10.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.10.0), [v0.11.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.11.0)

With access and basic context in place, attention turned to making answers
richer and more useful at a glance. The assistant began tracing how data
connects across systems, learned to introduce itself and the data landscape at
the start of a session, and showed its first interactive, visual results in
place of plain text.

- **Data lineage in context.** Answers now show where data comes from and what depends on it.
- **The assistant knows the platform.** It starts each session aware of which data and tools are available, which produces better, better-routed answers.
- **Interactive query results (first look).** An early version of visual, interactive result views instead of plain text, plus the ability to add custom interactive apps.

## Secure sign-in and business context
- Date Range: January 22 to 24, 2026
- Versions: [v0.2.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.2.0), [v0.3.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.3.0), [v0.3.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.3.1), [v0.4.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.4.0), [v0.4.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.4.1), [v0.4.3](https://github.com/txn2/mcp-data-platform/releases/tag/v0.4.3), [v0.5.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.5.0), [v0.6.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.6.0), [v0.7.0](https://github.com/txn2/mcp-data-platform/releases/tag/v0.7.0), [v0.7.1](https://github.com/txn2/mcp-data-platform/releases/tag/v0.7.1)

This is where the platform began. The earliest releases focused on the
essentials everything else builds on: getting people securely signed in with
their own accounts, and making sure the assistant's answers are grounded in your
organization's own definitions rather than raw database columns.

- **Single sign-on.** People sign in with their existing company accounts, and access is governed by their role from day one.
- **Answers come with business context.** When the assistant describes or queries a table, it automatically brings in the surrounding context (owners, descriptions, tags, and glossary terms) from the data catalog, so results are not just raw columns.
