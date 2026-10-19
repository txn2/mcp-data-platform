---
description: "Knowledge and memory in the portal: promoted pages, the catalog, the graph, insights, and captured memory."
---

# Knowledge and Memory

The Knowledge page is the single home for the **Memory to Insight to Knowledge** lifecycle. A short header teaches the model so a first-time reader can state what each stage is and how one becomes the next:

> Everything the platform learns is a **Memory**. Most memories are personal or operational and stay yours. When a memory asserts something true about the business or the data that others would benefit from, it becomes an **Insight**, a proposal awaiting review. Whoever holds the `apply_knowledge` capability reviews insights and promotes the good ones into **Knowledge**: shared, trusted, and canonical. Business and domain facts become knowledge pages; technical and entity facts go to the catalog.

The page has three tabs. Review and promote affordances appear only when your persona grants the `apply_knowledge` tool (a capability check, not an admin role).

Knowledge pages are facts to be searched and synthesized into new answers. A file you wrote and want reproduced verbatim belongs in [Resources](resources.md) instead, where its bytes and formatting survive; the knowledge pages empty state says so.

## Knowledge (default)

- **Unified search** - One query fans across every source you can access (the catalog and its glossary, tags and domains, canonical knowledge pages, your memory, captured insights, saved assets, uploaded resources, prompts, managed scripts, API endpoints, and connections) and returns results grouped by source with a coverage summary. It is the same federation behind the `search` tool, exposed over `GET /api/v1/portal/search`. It ranks semantically when an embedding provider is configured and degrades to lexical search otherwise
- **Browse** - With the search box empty, the tab browses the canonical knowledge pages. Personas with `apply_knowledge` can create, edit, and remove pages
- **Changesets** (`apply_knowledge` holders) - The record of insights promoted into knowledge: the catalog and knowledge-page changes applied when your agent runs `apply_knowledge`, with rollback to undo a changeset's writes. They live here, with the promoted knowledge, rather than with the unpromoted insights in the review pipeline

One query returns results grouped by source (catalog, knowledge pages, insights, memory, assets, prompts) with a per-source coverage summary and source filter chips. The coverage line reads "3 of 14 shown"; a source with more matches than the search ranked reads "3 of 25+ shown", because the count is a floor rather than a total and a bare 25 would say the list on screen is the whole of it.

![Unified search](../images/screenshots/light/user-knowledge-knowledge-light.webp#only-light)![Unified search](../images/screenshots/dark/user-knowledge-knowledge-dark.webp#only-dark)

### Built-in pages

The platform ships a set of its own knowledge pages — the rationale behind the advanced features that is not readable from tool schemas: writing managed scripts (the Starlark dialect's deliberate absences, DECIMAL columns arriving as strings, the save being the version that runs), script outputs and export identity (a stable name is a refresh, a dated name is an archive), the semi-dynamic dashboard pattern (`platform.publish_data` and the data region), asset references and the refresh loop (naming a file from an asset's content instead of carrying it, and refreshing that file without re-saving the document), provenance, call references, and the capture loop, the media type every stored file carries, and writing a document that fits the reader's screen (the viewport declaration an HTML asset owes, rows that wrap, and the one place a wide table may scroll). Each page states its mechanism as a mermaid diagram as well as in prose, rendered in the portal and read as a labeled graph by an agent that fetches the page. They are embedded in the binary and reconciled into the knowledge-page store at startup, so a release that changes them updates every deployment on its next start. Once reconciled they are ordinary pages: `search` ranks them, `fetch` dereferences them (by the page's slug as well as by its id, which is how the platform's own instruction baseline and `manage_script help` can name a page whose row id differs on every deployment), the portal renders them, and feedback threads work on them.

The pages cite one another, and **What the platform documents about itself** (`platform-index`) lists every one with a one-line summary, generated from the shipped set so it cannot leave one out. Each page ends with the pages a reader goes to next. A citation by slug is stored as a reference to that page's id on the deployment, so the portal links it and shows it as an edge in the graph and in the cited page's backlinks, in both directions; the references are derived on every start, after every page is written, so a deployment upgraded from a release that dropped them gains them with no page changed. The same holds for a deployment's own page citing a built-in one by slug.

Each carries a **Built-in** badge and is read-only where people edit: the portal offers no Edit, and the update paths refuse a change that the next start would overwrite anyway. A deployment that wants its own version of a topic hides the built-in page (**Hide**, the same action Remove is on an ordinary page) and writes its own — the startup reconcile respects the hide instead of resurrecting the page, and a deployment page holding the topic's slug is left alone. Hiding is not a one-way door: **Restore built-in** on the Knowledge list (apply_knowledge access, also `POST /api/v1/portal/knowledge-pages/restore-builtin`) brings hidden built-in pages back, refreshed to the running release; a hidden page whose topic a deployment page has since taken stays hidden, since that page owns the slug. An edited page reaches a deployment on its next start with nothing to restore, and an unchanged release writes no page, so a page's version history records exactly the releases that changed it, and a page a release stops shipping is retired everywhere — which, unlike a hide, does not survive the page being shipped again later.

### Cards and graph

The **Knowledge Pages** sub-tab offers two layouts of the same corpus, switched with the Cards/Graph toggle. Cards is the browse list. **Graph** draws the corpus as its reference network: every page is a node, so is every entity the pages reference (assets, prompts, collections, connections, managed scripts, catalog URNs, and other pages), and every edge is a stored reference, drawn as an arrow from the page to what it cites. A cited script also draws what its runs produced (#1985): an arrow from the script to each asset, collection or resource it wrote that you can open. The outputs you cannot open are not drawn or named; the inspector counts them on the script ("2 more outputs you cannot open"). Both cover the script's 50 most recent outputs, and the inspector says so when it wrote older ones too. Selecting a script lists its outputs under **Produced**, and selecting a file lists the script that wrote it under **Produced by**, apart from the pages that cite it.

**It opens on one node, not the whole corpus.** A whole-corpus force layout is a hairball that answers nothing, so the view starts at the corpus's strongest *bridge* — the node the most shortest paths run through — and shows its neighbourhood. **Hops** widens that neighbourhood one step at a time, and **Whole corpus** drops back to the overview when you want the shape of the entire knowledgebase.

The structure is measured, not just drawn. The corpus is partitioned into clusters (Louvain community detection) and each node is scored for how much of the graph it bridges (betweenness centrality). Node size is that bridge score, so the entities holding otherwise separate topics together are the largest marks on screen; node shape and colour stay with the type. In the whole-corpus overview each substantial cluster is tinted as a region behind its members, and the layout pulls a cluster's members together so those regions are actually distinct. The summary line states how many clusters were found and the partition's modularity, so you can tell whether the corpus really has topic structure rather than inferring it from a picture.

Clicking a node **inspects** it rather than navigating away. The inspector reports how many references run each way, the node's bridge score and its rank among everything that bridges anything, and its cluster; it lists both directions of references, each selectable in place so you can walk the corpus without a single page load. For anything that is not a knowledge page it also shows the reference's **URN** — the identifier the node's label is derived from, and the only string you can search or act on elsewhere. Its actions are **Focus** (re-centre the view here), **Expand** (pull in this node's neighbours), **Path from** (then click any other node to trace the shortest chain of references between them, highlighted on the canvas and listed hop by hop), and **Open**, which is the only thing that leaves the graph. A connection has no per-instance portal page, and a reference whose target has been removed has nothing left to open, so neither offers Open.

Selecting a **catalog** node looks the entity up in the platform catalog and shows what is actually there — its description, domain, owners, and tags. When the catalog does not have it, the inspector says so: a knowledge page citing a dataset that is not in your catalog is a real gap, not an ordinary node, and the graph states it rather than drawing it as if it resolved. **Open** deep-links the entity in the Knowledge > Catalog tab (`/knowledge/catalog?ref=...`), which is also what a catalog reference chip links to anywhere else in the portal.

Hovering a node lights its immediate neighbourhood and dims the rest; dragging pulls a cluster apart and it stays where you drop it (**Reset layout** releases every pin). Scroll to zoom and drag the canvas to pan. The **Show** chips filter by node type — knowledge pages are always shown, since they are the corpus the view is of — the tag facet narrows which pages are in the graph at all, and the search box focuses matching nodes rather than removing the rest. Switching between Cards and Graph preserves both the search text and the tag filter.

Entities you cannot access are absent from the graph entirely — neither node nor edge — which is the same visibility rule the per-page reference list applies; a script's outputs follow the same rule, counted rather than named. A very large corpus is capped, and the cap is always stated in a notice above the canvas rather than applied silently.

![Knowledge graph](../images/screenshots/light/user-knowledge-graph-light.webp#only-light)![Knowledge graph](../images/screenshots/dark/user-knowledge-graph-dark.webp#only-dark)

The whole-corpus overview, with the detected clusters drawn as regions:

![Knowledge graph, whole corpus](../images/screenshots/light/user-knowledge-graph-corpus-light.webp#only-light)![Knowledge graph, whole corpus](../images/screenshots/dark/user-knowledge-graph-corpus-dark.webp#only-dark)

### What a page links to

A knowledge page's **Manual references** panel is where an editor states what the page is about. It searches the portal's own entities — assets, collections, pages, prompts — and, for a reader who may read the catalog, its definitions: **glossary terms**, **tags**, and **domains**. Every type is searched by display name, so attaching the term *Net Revenue* never means typing the id the catalog gave it. A reader who may not read the catalog is offered only the four portal types, rather than three that could never return a match.

An attached reference renders as a named chip in the **Related** panel and links to where that entity is managed: a glossary term to the Glossary tab, a tag to Tags, a domain to Domains, a table to Tables. The names are resolved from the catalog itself, because the key inside a definition's reference is an id, not a name, so a chip built from the reference alone would read as `8f3c1a94` where the page meant *Net Revenue*. When the catalog cannot be read, the chip falls back to that key rather than failing the page.

A page can also cite a **managed script** (`mcp:script:<id>`), which is how a page that says which script keeps a dataset current names the script rather than describing it in prose. A script's definition is readable by everyone signed in (#2027), so every reader sees the citation in **Related** and as a chip in the body, linking to the script's page. Deleting the script removes the citation. Scripts are cited through `apply_knowledge` (`page.references`, or the reference written in the body); the Manual references picker does not search scripts.

The link runs both ways: each dataset and definition page lists the knowledge pages that reference its entity, so a steward reading a term sees what has been written about it. References you cannot access are omitted from both directions.

## Catalog

The **Catalog** sub-tab is the platform's own [data catalog](../server/catalog.md) inside the portal, and the second of the two knowledge sinks. It is shown to a reader whose persona grants `fetch`, the tool that reads the catalog over MCP; editing takes `apply_knowledge`, the tool that curates it. An administrator reaches everything.

![Catalog](../images/screenshots/light/user-knowledge-catalog-light.webp#only-light)![Catalog](../images/screenshots/dark/user-knowledge-catalog-dark.webp#only-dark)

Catalog holds its own inner tabs: **Tables**, **Glossary**, **Tags**, **Domains**, and **Data products** — the described things first, the vocabularies that describe them second. The section is URL-addressable at `/knowledge/catalog`, and the inner tab is carried in the hash (`/knowledge/catalog#tags`), so a refresh and browser back/forward land where you were. A single entity stays addressable at `/knowledge/catalog?ref=...`, which is what a catalog reference links to from anywhere in the portal; the link names the tab that manages that kind of entity (`?ref=mcp%3Aglossary_term%3A...#glossary`). Each tab claims only its own kinds, so a stale or hand-edited link opens the list rather than a read that could not succeed. Going back from a deep-linked entity drops the `?ref=` so a refresh does not reopen what you just left, and switching tab drops it too.

### Tables

The **Tables** tab searches the datasets of every connection your persona reaches, by text in the name, path or description, and filters to one connection. Opening one shows its description, tags, glossary terms, owners, domain and properties; its columns, each opened with **Edit** to change its own description, tags and glossary terms; its context documents (markdown notes with a title and a category, added, edited and removed in place); its lineage, each upstream and downstream dataset a link that opens it; the schema document an API or GraphQL operation was read from; the knowledge pages that cite it; and its history. With `apply_knowledge`, each facet is editable; otherwise the view is read-only with no edit controls. Tags, glossary terms and domains are chosen through name-search pickers: type `Reven` and select **Revenue**, and the reference is resolved for you; a tag may also be a new name. Descriptions and notes are markdown, edited in the split source/preview editor used for knowledge pages and prompts. Owners are whatever identifies them to your organization, an email address most often.

The **History** panel lists every change written to the dataset, newest first: which aspect, who wrote it, when, what it now holds and what it replaced. **Roll back** restores the value a change replaced and is recorded as a change of its own. A column's history is on its editor.

The catalog scan finds tables; this tab describes them. There is no table create or delete here.

### Glossary

The **Glossary** tab lists the top of the glossary — the terms and nodes filed under no node, each node with a count of its entries — or every term and node matching a filter. Opening a node shows its definition and what is filed under it; opening a term shows its definition, the node it sits under, the datasets and columns that carry it (those on connections your persona does not reach are counted, not named), and the knowledge pages that cite it. With `apply_knowledge` you create a term or a node at the top or under the node you have open, edit any definition, and delete one. Definitions are markdown.

### Tags, Domains and Data products

The **Tags**, **Domains** and **Data products** tabs list their definitions with their descriptions, filter by name, and open one to see what it means, what carries it, and its history. With `apply_knowledge` you create, describe and delete them.

A definition still in use is not deleted: a node with entries under it, and a term, tag or domain a dataset or column carries, is refused with what still uses it. Delete says so before you confirm.

### Catalog endpoints

These tabs are backed by the portal catalog API at `/api/v1/portal/catalog`, listed on [Platform Catalog](../server/catalog.md#portal-catalog-api). Reads take `fetch` on your persona and writes `apply_knowledge`, and a dataset, its columns and its schema document are read and written only on connections your persona reaches; both checks are enforced server-side whatever the UI shows, and every write is recorded in the audit log as an `apply_knowledge` call from the portal. A malformed value is `400`; a reference the catalog does not hold is `404`, which is how the portal says a cited dataset is not in the catalog rather than that the read failed; a delete of a definition still in use is `409`.

## Insights

The review pipeline for insights, which are the only memories that cross between users. A pending-review count is badged on the sidebar Knowledge item and the Insights tab so reviewers notice work without opening it.

- **Your insights** - The insights captured from your sessions, with status (pending, approved, applied, rejected) and relevance search
- **Review queue** (`apply_knowledge` holders) - Every user's captured insights. Approving and rejecting curates which insights are worth promoting; the actual promotion into durable knowledge happens when you ask your agent to run `apply_knowledge`, whose synthesize step gathers the approved insights and writes business and domain facts to knowledge pages and technical and entity facts to the catalog

![Insights](../images/screenshots/light/user-knowledge-insights-light.webp#only-light)![Insights](../images/screenshots/dark/user-knowledge-insights-dark.webp#only-dark)

## Memory

Memory is personal: this tab is scoped to your own records. The only memory that crosses to other users is an insight, reviewed in the Insights tab, and it crosses when it is applied: applying an insight writes it to a canonical sink and makes it findable by everyone through `search`, attributed to whoever captured it. An insight that is still pending or approved stays yours alone.

- **Your memory** - The raw substrate captured from your sessions, classified by lifecycle **class** (`sink_class`): Preference, Event, Business knowledge, Operational rule, and Schema/entity. The class is why something is "just memory" versus a candidate for promotion

![Memory](../images/screenshots/light/user-knowledge-memory-light.webp#only-light)![Memory](../images/screenshots/dark/user-knowledge-memory-dark.webp#only-dark)

The former Knowledge Pages, Knowledge & Memory, and admin Knowledge & Memory routes now redirect into this one page.

See [Knowledge Capture](../knowledge/overview.md) and [Memory Layer](../memory/overview.md) for how these are created during sessions.

