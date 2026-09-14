#!/usr/bin/env python3
"""Inject x-tagGroups into generated swagger spec for Redoc/Scalar tag grouping."""

import json
import re
import sys
from pathlib import Path

TAG_DESCRIPTIONS = {
    "User": (
        "Current user identity, roles, persona, and available tools."
    ),
    "Activity": (
        "Personal analytics for the authenticated user's tool usage. "
        "Timeseries, breakdowns, and summary statistics scoped to the calling user."
    ),
    "Assets": (
        "AI-generated assets — dashboards, reports, visualizations, and data exports. "
        "Supports HTML, JSX, SVG, Markdown, and CSV content types with versioning, "
        "thumbnails, and sharing."
    ),
    "Collections": (
        "Curated groups of assets organized into ordered sections with markdown descriptions. "
        "Collections support sharing via public links and user-level permissions."
    ),
    "Knowledge": (
        "Domain knowledge captured during AI sessions. Insights go through an admin review "
        "workflow before being written back to the data catalog. Includes insight statistics "
        "and governance lifecycle tracking."
    ),
    "Memory": (
        "Persistent memory records accumulated across sessions — corrections, preferences, "
        "business context, and data quality observations. Backed by PostgreSQL with pgvector "
        "for semantic search."
    ),
    "Prompts": (
        "Reusable prompt templates with argument placeholders. Users manage personal prompts "
        "and browse available global, persona, and system prompts."
    ),
    "Resources": (
        "Human-uploaded reference materials — SQL templates, runbooks, checklists, and brand "
        "assets. Scoped by visibility (global, persona, user) and accessible to AI agents "
        "via the MCP resources protocol."
    ),
    "Shares": (
        "Asset and collection sharing via public links (token-based, time-limited) "
        "and user shares (email-based with viewer/editor permissions)."
    ),
    "Audit": (
        "Platform-wide audit log of every tool call. Paginated event queries with filtering, "
        "aggregate statistics, performance percentiles, enrichment metrics, and discovery "
        "pattern analytics."
    ),
    "Auth Keys": (
        "API key management for programmatic access. Create, list, and revoke keys with "
        "role assignment and expiration. Keys from the config file are read-only."
    ),
    "Config": (
        "Platform configuration management. Read the active config, export as YAML, "
        "and manage per-key database overrides for whitelisted settings with hot-reload."
    ),
    "Connections": (
        "Toolkit connection management for Trino, DataHub, and S3 backends. "
        "View file-configured connections, create database-managed instances, and inspect "
        "connection details."
    ),
    "Personas": (
        "Role-based access control profiles that determine which tools and connections "
        "a user can access. Each persona defines allow/deny patterns, context overrides, "
        "and priority-based role mapping."
    ),
    "Calls": (
        "The data-access calls the platform recorded: every query and API invocation "
        "with the purpose stated for it, what it addressed, and an outcome derived from "
        "what was later built from it. A satisfied record can be promoted, which turns a "
        "query into a catalog Query entity and an API call into a saved example on its "
        "endpoint."
    ),
    "Sessions": (
        "Sessions read back from the audit log. A session is not a stored row: it is "
        "every tool call sharing one session id, so the list reaches as far back as "
        "audit retention. One session opens onto the assets and insights it produced "
        "and the ordered record of its calls, each with the purpose the agent stated."
    ),
    "Scripts": (
        "Managed-script review and approval. Browse scripts and their version history, "
        "read a version alongside the capabilities and connections its source reaches "
        "for, and approve a version — which binds the capability grant it executes "
        "under and is the only thing that makes a script executable."
    ),
    "System": (
        "Platform identity, version, runtime feature availability, registered tools, "
        "and toolkit connections."
    ),
    "Tools": (
        "Tool schema introspection and interactive execution. Browse JSON schemas for all "
        "registered tools and execute tool calls with parameter validation."
    ),
    "DataHub": (
        "The DataHub catalog surface behind the portal: search and browse entities, read "
        "and edit their descriptions, tags, owners, glossary terms and domain, and manage "
        "the glossary hierarchy and the governance vocabularies."
    ),
    "Tables": (
        "Query-engine tables registered against a managed resource or a portal asset, so a "
        "file's contents can be queried through the platform's SQL surface."
    ),
    "Gateway": (
        "The API gateway data plane: call a configured upstream connection over REST, "
        "enveloped or streamed. This is what a non-MCP client (NiFi, Airflow, curl) uses "
        "in place of the api_invoke_endpoint tool."
    ),
    "APIs": (
        "The caller's view of the API catalogs: browse the connections this identity may "
        "reach, read an operation's parameters and schemas, and copy the gateway call."
    ),
    "API Catalogs": (
        "Administration of the OpenAPI documents behind API connections. Register a spec "
        "inline, by URL, or by upload, refresh it, and manage its embedding jobs."
    ),
    "Portal": (
        "Portal surfaces that are not asset content: navigation, search, and the pages "
        "the web UI is assembled from."
    ),
    "Portal Assets": (
        "Asset operations served to the portal UI: references, attachments, versions, and "
        "the managed resources an asset's content points at."
    ),
    "Feedback": (
        "Review threads on assets and knowledge: comments, activity, worklists, sign-off, "
        "and capturing a thread's conclusion as an insight."
    ),
    "Notifications": (
        "Email notification preferences, delivery history, and per-user unsubscribe. "
        "Mail-server settings live under Settings."
    ),
    "Settings": (
        "Deployment settings an administrator edits at runtime: the mail server and the "
        "review-queue alert thresholds."
    ),
    "Users": (
        "The directory of known people, keyed by email. Administrative counterpart to the "
        "single-identity User tag."
    ),
}

TAG_GROUPS = [
    {
        "name": "User API",
        "tags": [
            "User",
            "Activity",
            "APIs",
            "Assets",
            "Collections",
            "DataHub",
            "Feedback",
            "Gateway",
            "Knowledge",
            "Memory",
            "Portal",
            "Portal Assets",
            "Prompts",
            "Resources",
            "Shares",
            "Tables",
        ],
    },
    {
        "name": "Admin API",
        "tags": [
            "API Catalogs",
            "Audit",
            "Auth Keys",
            "Calls",
            "Config",
            "Connections",
            "Notifications",
            "Personas",
            "Scripts",
            "Sessions",
            "Settings",
            "System",
            "Tools",
            "Users",
        ],
    },
]


def build_tags_array() -> list[dict]:
    return [{"name": name, "description": desc} for name, desc in TAG_DESCRIPTIONS.items()]


def patch_json(path: Path) -> None:
    spec = json.loads(path.read_text())
    spec["tags"] = build_tags_array()
    spec["x-tagGroups"] = TAG_GROUPS
    path.write_text(json.dumps(spec, indent=4) + "\n")


def patch_yaml(path: Path) -> None:
    text = path.read_text()
    # Build tags block
    tags_block = "\ntags:\n"
    for name, desc in TAG_DESCRIPTIONS.items():
        tags_block += f'  - name: "{name}"\n'
        tags_block += f'    description: "{desc}"\n'
    # Build x-tagGroups block
    groups_block = "\nx-tagGroups:\n"
    for group in TAG_GROUPS:
        groups_block += f'  - name: "{group["name"]}"\n'
        groups_block += "    tags:\n"
        for tag in group["tags"]:
            groups_block += f'      - "{tag}"\n'
    path.write_text(text.rstrip() + "\n" + tags_block + groups_block)


def patch_docs_go(path: Path) -> None:
    content = path.read_text()
    if '"securityDefinitions"' not in content:
        print("  docs.go: securityDefinitions not found, skipping")
        return
    tags_json = json.dumps(build_tags_array())
    tag_groups_json = json.dumps(TAG_GROUPS)
    insertion = f',"tags":{tags_json},"x-tagGroups":{tag_groups_json}'
    # The Go template is a backtick raw string: const docTemplate = `{...}`
    # Find the closing `}` + backtick that ends the template (on its own line).
    marker = "}`"
    idx = content.rfind(marker)
    if idx == -1:
        print("  docs.go: could not find template end, skipping")
        return
    content = content[:idx] + insertion + content[idx:]
    path.write_text(content)


def main() -> None:
    if len(sys.argv) < 2:
        print("Usage: swagger-tag-groups.py <apidocs-dir>")
        sys.exit(1)

    apidocs = Path(sys.argv[1])

    json_path = apidocs / "swagger.json"
    if json_path.exists():
        patch_json(json_path)
        print(f"  Patched {json_path}")

    yaml_path = apidocs / "swagger.yaml"
    if yaml_path.exists():
        patch_yaml(yaml_path)
        print(f"  Patched {yaml_path}")

    docs_path = apidocs / "docs.go"
    if docs_path.exists():
        patch_docs_go(docs_path)
        print(f"  Patched {docs_path}")


if __name__ == "__main__":
    main()
