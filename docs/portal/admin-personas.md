---
description: "The persona editor: one page grouped by surface, with the server-computed access summary, tool and connection rules, API endpoint rules, instructions and portal access."
---

# Personas

The Personas page manages role-based tool access rules and context overrides using the same split-pane layout as Connections.

![Personas](../images/screenshots/light/admin-admin-personas-light.webp#only-light)![Personas](../images/screenshots/dark/admin-admin-personas-dark.webp#only-dark)

Creating or editing a persona opens one page of sections, grouped by the surface each setting governs (#2257). The index on the left jumps between them.

| Section | Group | Governs |
|---|---|---|
| Identity | | Who gets the persona: name, display name, roles, the roles role mapping assigns it (read-only), priority, description, and account type (Internal people, External people, Automated caller). Grants nothing. |
| Access summary | | What the persona reaches, computed by the server from the page as it stands, saved or not, with what is granted to it elsewhere and what no persona setting governs. |
| Tools | AI assistant (MCP) | The MCP tools its people can list and call, and the REST gateway that runs through them. |
| Connections | AI assistant (MCP) | The data sources tool calls reach, and what search, the catalog and scratch tables show. |
| API endpoints | AI assistant (MCP) | The methods and paths `api_invoke_endpoint` may call on API connections. |
| Instructions | AI assistant (MCP) | The text `platform_info` returns. Grants nothing. |
| Portal access | Portal (browser) | The portal's pages and the portal API behind them, scratch-table routes included. |

![Access summary](../images/screenshots/light/admin-admin-persona-summary-light.webp#only-light)![Access summary](../images/screenshots/dark/admin-admin-persona-summary-dark.webp#only-dark)

The **Identity** section warns when another persona lists one of the persona's roles (and which wins for a person holding it), when an external persona's role is one internal people hold, and that a role shared with another persona reads that persona's folder and prompts either way. On the administrator persona it shows an **Administrator persona** badge, External is not offered, and Delete is not offered.

**Tools** and **Connections** list every tool and connection with the server's decision on it, and three controls per row, **Allow**, **No rule** and **Deny**, which write the row's own name to the persona's lists. A deny pattern still wins over an allow: on a row a deny pattern matches, Allow is disabled and names the pattern. The patterns themselves are edited in the collapsed **Pattern rules** panel above the rows. Tools offers four starting points: **All tools**, **Read only**, **Analyst** and **Clear**; none makes anyone an administrator, which only the administrator persona does. A scratch connection carries its catalog and schema, and a warning when the persona can drop any registered table on it through SQL. On an external persona the rows are shown but locked: its rules are kept for if it becomes internal again.

![New Persona](../images/screenshots/light/admin-admin-persona-create-light.webp#only-light)![New Persona](../images/screenshots/dark/admin-admin-persona-create-dark.webp#only-dark)

**API endpoints** lists the `api` kind connections this deployment serves and, under each, the operations its catalog declares, with the persona's decision on every one. Selecting an operation writes a rule naming that operation's own method and the path its catalog declares, so a rule written here is the same rule a config file would write; the rule editor beside it takes a pattern by hand for a path no indexed operation corresponds to. An operation no rule names is marked reachable rather than allowed, because on this axis a connection no rule names keeps full access. An operation whose connection is not allowed, whose persona cannot call `api_invoke_endpoint`, or whose persona is external says so on its row.

![Persona API endpoint rules](../images/screenshots/light/admin-admin-persona-api-routes-light.webp#only-light)![Persona API endpoint rules](../images/screenshots/dark/admin-admin-persona-api-routes-dark.webp#only-dark)

**Instructions** holds two texts, **Tool descriptions** and **Agent instructions**, each either added to the platform's text or replacing it.

**Portal access** sets one level per portal area. Each control's help names the AI-side tool that is governed elsewhere. On an external persona the rows External sets are locked, each with the saved choice beside what applies ("Set by External: None. Saved choice: Edit."), Resources offers None or the persona's own folder, and an **Uploads** setting names who each upload is shared with. On the administrator persona every row is locked: administrators have every area.

![External persona](../images/screenshots/light/admin-admin-persona-external-light.webp#only-light)![External persona](../images/screenshots/dark/admin-admin-persona-external-dark.webp#only-dark)

Deleting a persona lists what still names it, stored secrets, automation run grants, prompts, shares and the rest, and deletes with them left in place once confirmed.

![Deleting a persona](../images/screenshots/light/admin-admin-persona-delete-light.webp#only-light)![Deleting a persona](../images/screenshots/dark/admin-admin-persona-delete-dark.webp#only-dark)

See [Personas](../personas/overview.md) for configuration details and [API endpoint rules](../personas/overview.md#api-endpoint-rules) for how a rule is evaluated.

