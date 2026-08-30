---
description: API keys and the people directory.
---

# Keys and Users

Who and what may reach the platform: the keys programmatic callers present, and the people it knows.

## API Keys

The Keys page manages API keys for programmatic authentication.

![API Keys](../images/screenshots/light/admin-admin-keys-light.webp#only-light)![API Keys](../images/screenshots/dark/admin-admin-keys-dark.webp#only-dark)

The add-key form collects a name, optional owner email and description, roles (with a role browser), and an expiration. The generated key is shown once in a copy-now banner and never again.

![Add API Key](../images/screenshots/light/admin-admin-key-create-light.webp#only-light)![Add API Key](../images/screenshots/dark/admin-admin-key-create-dark.webp#only-dark)

Features:

- **Key table** — Name, source badge (file/database), email, description, roles badge, expiration date, and actions
- **Expired keys** — Shown with dimmed text and "Expired" badge
- **+ Add Key** — Create keys with name, email, description, roles, and expiration preset (Never, 24h, 7d, 30d, 90d, 1yr). The plaintext key is shown only once at creation.
- **Delete** — Available for database-managed keys only; file keys are read-only
- **Source badges** — Same file/database/both system as Connections

## Users

The Users page manages the known-users directory: a record of people (first
name, last name, email) used to make sharing easier. It is not an
authorization layer and grants no access; it only gives the share picker names
to resolve and suggest.

![Users](../images/screenshots/light/admin-admin-users-light.webp#only-light)![Users](../images/screenshots/dark/admin-admin-users-dark.webp#only-dark)

Features:

- **User table** — Name, email, status badge, and last-seen date
- **Status badge** — **Active** (green) for someone seen via a real sign-in, or **Invited** (amber) for someone an admin pre-added who has not logged in yet
- **+ Add User** — Pre-add a person by email (with optional first and last name) so they are selectable for sharing before they have ever signed in
- **Edit** — Change a person's first and last name. Admin-entered names take precedence: a later sign-in only fills blank name fields, it never overwrites a name an admin set
- **Search** — Filter the directory by name or email
- **Auto-recording** — Anyone who authenticates (OIDC/OAuth) is upserted into the directory automatically with the name from their token claims; API-key and anonymous sessions are not recorded

Requires a database. Without one the directory is disabled and the share
dialog falls back to free-typed email only.

