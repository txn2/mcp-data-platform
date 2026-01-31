# MCP Apps Guide

MCP Apps provide interactive UI components that enhance tool results. This guide covers development, customization, and deployment.

## Overview

MCP Apps are HTML/JS/CSS files served as MCP resources. When a tool like `trino_query` returns data, an MCP Apps-compatible host can render an interactive UI instead of raw JSON.

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      MCP Host (Claude Desktop)               │
│  ┌─────────────┐    ┌──────────────┐    ┌───────────────┐  │
│  │  Tool Call  │───▶│  MCP Server  │───▶│  UI Resource  │  │
│  │ trino_query │    │  (platform)  │    │ ui://query... │  │
│  └─────────────┘    └──────────────┘    └───────┬───────┘  │
│                                                   │          │
│                      ┌────────────────────────────▼────────┐│
│                      │        Interactive UI (iframe)      ││
│                      │  ┌────────────────────────────────┐ ││
│                      │  │  Chart  │  Table  │  Filter    │ ││
│                      │  └────────────────────────────────┘ ││
│                      └─────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
```

### App Discovery

Apps are loaded from the filesystem at startup. The `assets_path` in configuration must be an absolute path to a directory containing the app's HTML/JS/CSS files.

## Quick Start (Development)

### Prerequisites

- Docker (only dependency needed)

### 1. Start Development Environment

The fully containerized setup includes Trino, the MCP server, and MCP Inspector:

```bash
# Clone the repository
git clone https://github.com/txn2/mcp-data-platform
cd mcp-data-platform

# Start everything in containers
docker compose -f docker-compose.dev.yml up
```

This starts:
- **Trino SQL engine** on port 8090 (also available internally)
- **MCP server** on port 3001
- **MCP Inspector** on port 6274
- Apps served from `./apps/` directory (mounted as volume)

### 2. Open MCP Inspector

Open http://localhost:6274 in your browser.

In the inspector:
1. Go to "Tools" → select `trino_query`
2. Enter test SQL:
   ```json
   {"sql": "SELECT 1 as id, 'Product A' as name, 15000.50 as revenue UNION ALL SELECT 2, 'Product B', 23000.75"}
   ```
3. Execute and observe the interactive result

### 3. Edit Apps Live

Edit files in `./apps/query-results/`:
- `index.html` - Main app file
- Changes are served immediately (no container restart needed)
- Refresh the inspector to see changes

### Alternative: Local Go Development

If you prefer running the server locally:

```bash
# Start only Trino
docker compose -f docker-compose.dev.yml up trino

# Run server locally
export MCP_APPS_PATH=$(pwd)/apps
go run ./cmd/mcp-data-platform --config configs/mcpapps-dev.yaml

# Test with inspector
npx @anthropic-ai/mcp-inspector http://localhost:3001/sse
```

## Configuration

### Basic Configuration

```yaml
mcpapps:
  enabled: true
  apps:
    query_results:
      enabled: true
      assets_path: "/path/to/apps/query-results"  # Absolute path required
      entry_point: "index.html"                    # Default: index.html
      tools:
        - trino_query
      csp:
        resource_domains:
          - "https://cdn.jsdelivr.net"
      config:
        chartCDN: "https://cdn.jsdelivr.net/npm/chart.js"
        maxTableRows: 1000
```

### Configuration Fields

| Field | Required | Description |
|-------|----------|-------------|
| `enabled` | Yes | Enable/disable this app |
| `assets_path` | Yes | Absolute path to app directory |
| `entry_point` | No | Main HTML file (default: `index.html`) |
| `resource_uri` | No | MCP resource URI (default: `ui://<app-name>`) |
| `tools` | Yes | List of tool names this app enhances |
| `csp.resource_domains` | No | Allowed CDN origins |
| `config` | No | App-specific config injected as JSON |

### Config Injection

The `config` object is injected into the HTML as a `<script id="app-config">` tag:

```html
<script id="app-config" type="application/json">{"chartCDN":"...","maxTableRows":1000}</script>
```

Access it in JavaScript:

```javascript
const config = JSON.parse(document.getElementById('app-config').textContent);
console.log(config.chartCDN);
```

## Creating Custom Apps

### App Structure

```
my-app/
├── index.html    # Entry point (required)
├── styles.css    # Optional stylesheets
├── app.js        # Optional scripts
└── assets/       # Optional images, fonts, etc.
```

### MCP Apps Protocol

Apps communicate with the host via `postMessage`:

```javascript
// Initialize
window.parent.postMessage({
  jsonrpc: '2.0',
  id: 1,
  method: 'ui/initialize',
  params: {
    protocolVersion: '2025-01-09',
    appInfo: { name: 'My App', version: '1.0.0' },
    appCapabilities: {
      availableDisplayModes: ['inline', 'fullscreen']
    }
  }
}, '*');

// Listen for tool results
window.addEventListener('message', (event) => {
  if (event.data?.method === 'ui/notifications/tool-result') {
    const result = event.data.params;
    // Handle the tool result
  }
});
```

### Example: Simple Results Viewer

```html
<!DOCTYPE html>
<html>
<head>
  <title>Results Viewer</title>
  <script id="app-config" type="application/json">{}</script>
</head>
<body>
  <div id="results"></div>
  <script>
    const config = JSON.parse(document.getElementById('app-config').textContent);

    window.addEventListener('message', (event) => {
      if (event.data?.method === 'ui/notifications/tool-result') {
        const data = JSON.parse(event.data.params.content[0].text);
        document.getElementById('results').innerHTML =
          `<pre>${JSON.stringify(data, null, 2)}</pre>`;
      }
    });

    // Initialize
    window.parent.postMessage({
      jsonrpc: '2.0',
      id: 1,
      method: 'ui/initialize',
      params: { protocolVersion: '2025-01-09' }
    }, '*');
  </script>
</body>
</html>
```

## Deployment

### Docker

Using bundled apps:

```bash
docker run -p 8080:8080 \
  -e TRINO_HOST=trino \
  ghcr.io/txn2/mcp-data-platform \
  --config /usr/share/mcp-data-platform/configs/mcpapps-docker.yaml
```

Using custom apps:

```bash
docker run -p 8080:8080 \
  -v $(pwd)/my-apps:/etc/mcp-apps \
  -v $(pwd)/my-config.yaml:/config.yaml \
  ghcr.io/txn2/mcp-data-platform \
  --config /config.yaml
```

### Kubernetes

See `configs/examples/kubernetes/` for complete examples.

**Using ConfigMaps for small apps:**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: mcp-apps-custom
data:
  index.html: |
    <!DOCTYPE html>
    <!-- Your app HTML -->
```

**Volume mount in deployment:**

```yaml
volumeMounts:
  - name: custom-apps
    mountPath: /etc/mcp-apps/my-app
volumes:
  - name: custom-apps
    configMap:
      name: mcp-apps-custom
```

### Bundled Apps Location

In Docker images, bundled apps are at:
```
/usr/share/mcp-data-platform/apps/
├── query-results/
│   └── index.html
└── (future apps)
```

## Security

### Content Security Policy

Apps can declare required CSP domains:

```yaml
csp:
  resource_domains:     # script-src, img-src, style-src, font-src
    - "https://cdn.jsdelivr.net"
  connect_domains:      # fetch/XHR endpoints
    - "https://api.example.com"
  frame_domains:        # nested iframes
    - "https://embed.example.com"
```

### Path Traversal Protection

The server validates that all asset requests stay within the app's `assets_path`. Requests like `../../../etc/passwd` are rejected.

### Sandboxing

MCP Apps run in sandboxed iframes. The host controls:
- iframe sandbox attributes
- CSP headers
- Communication via postMessage only

## Troubleshooting

### App Not Loading

1. Check `assets_path` is absolute and exists
2. Verify `entry_point` file exists
3. Check server logs for validation errors

### Changes Not Appearing

- Assets are read on each request (no caching)
- Ensure you're editing the correct file
- Hard refresh the browser (Cmd+Shift+R)

### CSP Errors

Check browser console for CSP violations. Add required domains to `csp.resource_domains`.

### Tool Not Enhanced

Verify the tool name in `tools` list matches exactly (e.g., `trino_query` not `query`).
