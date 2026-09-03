---
description: Deployment settings, agent instructions, the deployment description, the change log, and running the portal locally.
---

# Settings and Change Log

What the deployment says about itself, what it tells an agent, and the record of every change either produced.

## Agent Instructions

The Agent Instructions page edits the operating guidance every agent session receives. The editor is a split markdown view (source on the left, live preview on the right); a **Database override** badge appears when the value is stored in the database rather than the config file.

![Agent Instructions](../images/screenshots/light/admin-admin-agent-instructions-light.webp#only-light)![Agent Instructions](../images/screenshots/dark/admin-admin-agent-instructions-dark.webp#only-dark)

Above the editor sits the read-only **Platform baseline**: the platform-owned "how to operate" guidance composed beneath your instructions. It names only the tools this deployment exposes (search, query, save, capture), so you can see what is already covered and add only your business and deployment context on top.

Beside the Save button a **size meter** reports what this value costs, in bytes, against the limit the write path enforces. Every session on the deployment reads this layer in its first response, so it is bounded: past the advisory the page says the layer has grown from a set of rules into a document and names knowledge pages as the home for the overflow, and past the limit the save is refused. Both numbers come from the server, so the meter and the write path cannot disagree. See [Configuration](../server/configuration.md#the-customized-layer-is-byte-bounded) for the values, and [Knowledge](../knowledge/overview.md#the-third-sink-the-deployments-own-operating-rules) for the `apply_knowledge` sink that writes this same layer from a reviewed capture.

## Description

The Description page sets the platform's identity string, surfaced to MCP clients (for example in `platform_info`). Same split markdown editor and database-override semantics as Agent Instructions.

![Description](../images/screenshots/light/admin-admin-description-light.webp#only-light)![Description](../images/screenshots/dark/admin-admin-description-dark.webp#only-dark)

## Settings

The Settings page holds global platform settings. The first section is
**Email (SMTP)**, which configures outbound mail for [email
notifications](../server/notifications.md). Host, port, credentials, sender address,
and TLS mode are stored in the database (the password encrypted at rest
and write-only), and a **Send test** action verifies the configuration by
delivering a test email; when the target address has opted out of
notification emails, an informational notice appears next to the send
action (the test still sends). Like other admin configuration, editing
requires database config mode. Email branding (footer text, legal links,
Reply-To) is implementor-owned YAML, not part of this page; see the
[portal configuration](../server/configuration.md).

A **Knowledge review queue alert** section follows: it decides when the
platform emails an operator about unreviewed insights — the pending-count
and oldest-age thresholds, the re-alert cooldown, and the recipients. A
section that would deliver nothing (enabled with no recipients, or with
both thresholds cleared) says so in a banner rather than saving silently.
See [review queue alerts](../server/notifications.md#review-queue-alerts).

## Change Log

The Change Log page provides an audit trail of all configuration changes made via the admin UI.

![Change Log](../images/screenshots/light/admin-admin-changelog-light.webp#only-light)![Change Log](../images/screenshots/dark/admin-admin-changelog-dark.webp#only-dark)

Each entry shows:

- **Config key** — The configuration path that changed (e.g., `server.description`, `server.agent_instructions`)
- **Action** — Set (red badge) indicating a value was written
- **Timestamp** — When the change was made

## Local Development

Run the portal locally with demo data using [Mock Service Worker](https://mswjs.io/):

```bash
cd ui
npm install
VITE_MSW=true npm run dev
```

Open `http://localhost:5173/portal/` — no backend required. The mock data includes realistic ACME Corporation demo content with 200+ audit events, 50 knowledge insights, 6 personas, and 12 users.

For full-stack development with a real backend:

```bash
make dev-up                                        # Start PostgreSQL
go run ./cmd/mcp-data-platform --config dev/platform.yaml  # Start server
psql -h localhost -U platform -d mcp_platform -f dev/seed.sql  # Seed demo data
cd ui && npm run dev                               # Start React dev server
```

See [`dev/README.md`](https://github.com/txn2/mcp-data-platform/blob/main/dev/README.md) for complete local development instructions.

### Generating Screenshots

Automated screenshot generation captures every portal page in light and dark modes:

```bash
cd ui
npm run screenshots              # Generate portal PNG screenshots
npm run screenshots:apps         # Generate MCP App PNG screenshots
npm run screenshots:convert      # Convert to optimized WebP
```

Screenshots are saved to `docs/images/screenshots/light/` and `docs/images/screenshots/dark/`. See `ui/e2e/screenshots/README.md` for configuration options including custom branding.

