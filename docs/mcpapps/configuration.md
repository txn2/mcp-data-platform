# MCP Apps Configuration

MCP Apps are **enabled by default**. The built-in `platform-info` app registers automatically at startup — no configuration required.

## Built-in App: platform-info

`platform-info` is embedded in the binary and requires no `assets_path`, volume mount, or explicit `enabled: true`. It is always available unless MCP Apps are explicitly disabled.

### Branding (optional)

Override branding without replacing any HTML:

```yaml
mcpapps:
  apps:
    platform-info:
      config:
        brand_name: "ACME Data Platform"
        brand_url: "https://data.acme.com"
        logo_svg: "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 40 40'>...</svg>"
```

All fields are optional. When unset, the app falls back to the server name and a default logo.

### Custom HTML Override

To replace the embedded HTML entirely with your own version:

```yaml
mcpapps:
  apps:
    platform-info:
      assets_path: "/etc/mcp-apps/platform-info"   # overrides embedded content
      config:
        brand_name: "ACME Data Platform"
```

### Disable platform-info

```yaml
mcpapps:
  enabled: false   # disables all MCP Apps including platform-info
```

## Disabling MCP Apps

```yaml
mcpapps:
  enabled: false
```

## Custom Apps

Register additional apps alongside the built-in ones:

```yaml
mcpapps:
  apps:
    query_results:
      enabled: true
      assets_path: "/etc/mcp-apps/query-results"
      tools:
        - trino_query
        - trino_execute
      csp:
        resource_domains:
          - "https://cdn.jsdelivr.net"
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | No | Master switch for all MCP Apps (default: `true`) |
| `apps.<name>.enabled` | bool | No | Enable this app (default: `true`) |
| `apps.<name>.assets_path` | string | No* | Absolute path to app HTML/JS/CSS directory. *Required for custom apps; optional for `platform-info` (uses embedded HTML by default) |
| `apps.<name>.entry_point` | string | No | HTML entry point filename (default: `index.html`) |
| `apps.<name>.resource_uri` | string | No | MCP resource URI (default: `ui://<app_name>`) |
| `apps.<name>.tools` | array | Yes | Tools this app enhances |
| `apps.<name>.csp.resource_domains` | array | No | Allowed CDN origins for scripts/styles |
| `apps.<name>.csp.connect_domains` | array | No | Allowed fetch/XHR endpoints |
| `apps.<name>.config` | object | No | App-specific config injected as `<script id="app-config">` JSON |

## Using the query-results Example App

The repository includes a community example app at `apps/query-results/`. To use it:

### Docker

```bash
docker run -p 8080:8080 \
  -v $(pwd)/apps/query-results:/etc/mcp-apps/query-results \
  -v $(pwd)/config.yaml:/config.yaml \
  ghcr.io/txn2/mcp-data-platform \
  --config /config.yaml
```

`config.yaml`:

```yaml
server:
  transport: http
  address: ":8080"

mcpapps:
  apps:
    query_results:
      enabled: true
      assets_path: "/etc/mcp-apps/query-results"
      tools:
        - trino_query
      csp:
        resource_domains:
          - "https://cdn.jsdelivr.net"
```

### Kubernetes

Use a ConfigMap to deploy a custom app:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: mcp-app-query-results
data:
  index.html: |
    <!DOCTYPE html>
    <!-- Copy contents from apps/query-results/index.html -->
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-data-platform
spec:
  template:
    spec:
      containers:
        - name: mcp-data-platform
          volumeMounts:
            - name: query-results-app
              mountPath: /etc/mcp-apps/query-results
      volumes:
        - name: query-results-app
          configMap:
            name: mcp-app-query-results
```

For `platform-info`, no ConfigMap or volume mount is needed — the HTML is embedded in the binary.

## Config Injection

The `config` object is injected into the app's HTML as a script tag:

```html
<script id="app-config" type="application/json">{"brand_name":"ACME","brand_url":"https://acme.com"}</script>
```

Access in JavaScript:

```javascript
const config = JSON.parse(document.getElementById('app-config').textContent);
```

## Security

### Content Security Policy

Apps declare required CSP domains in config. The server enforces these, preventing apps from loading resources from unauthorized origins.

### Path Traversal Protection

Asset requests are validated to stay within `assets_path`. Requests for `../../../etc/passwd` are rejected. Embedded apps use `fs.FS` which provides the same protection natively.

### Sandboxing

Apps run in sandboxed iframes controlled by the MCP host. They cannot access the parent page or other apps.

## Troubleshooting

| Problem | Solution |
|---------|----------|
| `platform-info` not appearing | Check that MCP Apps are not explicitly disabled (`mcpapps.enabled: false`) |
| Custom app not loading | Check `assets_path` is absolute and exists |
| CSP errors in console | Add required domains to `csp.resource_domains` |
| Tool not enhanced | Verify tool name matches exactly (e.g., `trino_query`) |
| Config not injected | Ensure `config` is valid JSON-serializable YAML |
