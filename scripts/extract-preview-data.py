#!/usr/bin/env python3
"""
Extract platform_info preview data from a platform YAML or Kubernetes ConfigMap YAML.
Writes apps/preview-data.json for consumption by the test-harness.

Usage: python3 scripts/extract-preview-data.py <config-path> <output-path>
Requires: pip3 install pyyaml
"""
import yaml, json, sys


def main():
    if len(sys.argv) < 3:
        print("Usage: extract-preview-data.py <config-path> <output-path>", file=sys.stderr)
        sys.exit(1)

    with open(sys.argv[1]) as f:
        doc = yaml.safe_load(f)

    # Unwrap Kubernetes ConfigMap
    if isinstance(doc, dict) and doc.get('kind') == 'ConfigMap':
        doc = yaml.safe_load(doc.get('data', {}).get('platform.yaml', '{}'))

    srv    = doc.get('server', {}) or {}
    inj    = doc.get('injection', {}) or {}
    aud    = doc.get('audit', {}) or {}
    kn     = doc.get('knowledge', {}) or {}
    pi_cfg = ((doc.get('mcpapps') or {}).get('apps', {}) or {}) \
                 .get('platform-info', {}).get('config', {}) or {}

    toolkits_cfg = doc.get('toolkits') or {}
    toolkit_descriptions = {
        k: v.get('description', '')
        for k, v in toolkits_cfg.items()
        if isinstance(v, dict) and v.get('description', '').strip()
    }

    result = {
        'tool_result': {
            'name':               srv.get('name', ''),
            'version':            srv.get('version', ''),
            'description':        srv.get('description', ''),
            'tags':               srv.get('tags') or [],
            'agent_instructions': srv.get('agent_instructions', ''),
            'toolkits':           list(toolkits_cfg.keys()),
            'toolkit_descriptions': toolkit_descriptions or None,
            'features': {
                'semantic_enrichment': bool(inj.get('trino_semantic_enrichment') or inj.get('s3_semantic_enrichment')),
                'query_enrichment':    bool(inj.get('datahub_query_enrichment')),
                'storage_enrichment':  bool(inj.get('datahub_storage_enrichment')),
                'audit_logging':       bool(aud.get('enabled')),
                'knowledge_capture':   bool(kn.get('enabled')),
            },
            'config_version': {
                'api_version':        doc.get('api_version', 'v1'),
                'latest_version':     'v1',
                'supported_versions': ['v1'],
            },
        },
        'branding': {
            'brand_name': pi_cfg.get('brand_name', ''),
            'brand_url':  pi_cfg.get('brand_url', ''),
            'logo_svg':   pi_cfg.get('logo_svg', ''),
        },
    }

    with open(sys.argv[2], 'w') as f:
        json.dump(result, f, indent=2)
    print(f"Preview data written to {sys.argv[2]}")


if __name__ == '__main__':
    main()
