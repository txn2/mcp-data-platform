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
        "AI-generated artifacts — dashboards, reports, visualizations, and data exports. "
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
    "System": (
        "Platform identity, version, runtime feature availability, registered tools, "
        "and toolkit connections."
    ),
    "Tools": (
        "Tool schema introspection and interactive execution. Browse JSON schemas for all "
        "registered tools and execute tool calls with parameter validation."
    ),
}

TAG_GROUPS = [
    {
        "name": "User API",
        "tags": [
            "User",
            "Activity",
            "Assets",
            "Collections",
            "Knowledge",
            "Memory",
            "Prompts",
            "Resources",
            "Shares",
        ],
    },
    {
        "name": "Admin API",
        "tags": [
            "Audit",
            "Auth Keys",
            "Config",
            "Connections",
            "Personas",
            "System",
            "Tools",
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
