# MCP Apps Configuration

MCP Apps must be explicitly enabled and configured. The platform provides the infrastructure; you provide the apps.

## Basic Configuration

```yaml
mcpapps:
  enabled: true
  apps:
    query_results:
      enabled: true
      assets_path: "/etc/mcp-apps/query-results"
      tools:
        - trino_query
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | No | Enable MCP Apps infrastructure (default: false) |
| `apps` | map | Yes | Named app configurations |

## App Configuration

Each app requires a unique name and configuration:

```yaml
mcpapps:
  apps:
    my_app:
      enabled: true
      assets_path: "/absolute/path/to/app"
      entry_point: "index.html"
      resource_uri: "ui://my_app"
      tools:
        - trino_query
      csp:
        resource_domains:
          - "https://cdn.jsdelivr.net"
        connect_domains:
          - "https://api.example.com"
      config:
        maxRows: 1000
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | No | Enable this app (default: true) |
| `assets_path` | string | Yes | Absolute path to app directory |
| `entry_point` | string | No | HTML entry point (default: index.html) |
| `resource_uri` | string | No | MCP resource URI (default: ui://<app_name>) |
| `tools` | array | Yes | Tools this app enhances |
| `csp.resource_domains` | array | No | Allowed CDN origins for scripts/styles |
| `csp.connect_domains` | array | No | Allowed fetch/XHR endpoints |
| `config` | object | No | App-specific config injected as JSON |

## Using the Example App

The repository includes an example app at `apps/query-results/`. To use it:

### Docker

```bash
docker run -p 8080:8080 \
  -v $(pwd)/apps/query-results:/etc/mcp-apps/query-results \
  -v $(pwd)/config.yaml:/config.yaml \
  ghcr.io/txn2/mcp-data-platform \
  --config /config.yaml
```

Your `config.yaml`:

```yaml
server:
  transport: http
  address: ":8080"

mcpapps:
  enabled: true
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

Use ConfigMaps to deploy apps:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: mcp-app-query-results
data:
  index.html: |
    <!DOCTYPE html>
    <html>
    <!-- Copy contents from apps/query-results/index.html -->
    </html>
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

## Config Injection

The `config` object is injected into your app's HTML as a script tag:

```html
<script id="app-config" type="application/json">{"maxRows":1000}</script>
```

Access in JavaScript:

```javascript
const config = JSON.parse(document.getElementById('app-config').textContent);
```

## Multiple Apps

Configure multiple apps for different tools:

```yaml
mcpapps:
  enabled: true
  apps:
    query_results:
      enabled: true
      assets_path: "/etc/mcp-apps/query-results"
      tools:
        - trino_query

    s3_browser:
      enabled: true
      assets_path: "/etc/mcp-apps/s3-browser"
      tools:
        - s3_list_objects
        - s3_get_object
```

## Security

### Content Security Policy

Apps declare required CSP domains in config. The server enforces these restrictions, preventing apps from loading resources from unauthorized origins.

### Path Traversal Protection

Asset requests are validated to stay within `assets_path`. Requests like `../../../etc/passwd` are rejected.

### Sandboxing

Apps run in sandboxed iframes controlled by the MCP host. They cannot access the parent page or other apps.

## Troubleshooting

| Problem | Solution |
|---------|----------|
| App not loading | Check `assets_path` is absolute and exists |
| CSP errors in console | Add required domains to `csp.resource_domains` |
| Tool not enhanced | Verify tool name matches exactly (e.g., `trino_query`) |
| Config not injected | Ensure `config` is valid JSON in your YAML |
