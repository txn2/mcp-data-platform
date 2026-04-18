#!/usr/bin/env python3
"""Inject x-tagGroups into generated swagger spec for Redoc/Scalar tag grouping."""

import json
import re
import sys
from pathlib import Path

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


def patch_json(path: Path) -> None:
    spec = json.loads(path.read_text())
    spec["x-tagGroups"] = TAG_GROUPS
    path.write_text(json.dumps(spec, indent=4) + "\n")


def patch_yaml(path: Path) -> None:
    lines = path.read_text()
    # Append x-tagGroups at the end
    yaml_block = "\nx-tagGroups:\n"
    for group in TAG_GROUPS:
        yaml_block += f'  - name: "{group["name"]}"\n'
        yaml_block += "    tags:\n"
        for tag in group["tags"]:
            yaml_block += f'      - "{tag}"\n'
    path.write_text(lines.rstrip() + "\n" + yaml_block)


def patch_docs_go(path: Path) -> None:
    content = path.read_text()
    # The docs.go file has the spec as a JSON string in SwaggerInfo.SwaggerTemplate.
    # We need to inject x-tagGroups into that JSON string.
    # Find the closing brace of the top-level JSON object in the template.
    tag_groups_json = json.dumps(TAG_GROUPS)
    # Replace the last } in the swagger template with ,"x-tagGroups":[...]}
    # The template is a raw string literal assigned to SwaggerTemplate.
    old = '"securityDefinitions"'
    if old not in content:
        print("  docs.go: securityDefinitions not found, skipping")
        return
    # Insert x-tagGroups after the info block by finding the JSON and re-serializing
    # Actually, the simplest approach: find `}` at end of the JSON template and insert before it
    # The template ends with `}`
    insertion = f',"x-tagGroups":{tag_groups_json}'
    # Find the last occurrence of `}` followed by `"` (end of JSON in Go string)
    content = re.sub(r'}\s*"\s*$', insertion + '}"', content, count=1)
    # Fallback: if the above didn't match, try multiline
    if insertion not in content:
        content = re.sub(r'}\s*`\s*$', insertion + '}`', content, count=1, flags=re.MULTILINE)
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
