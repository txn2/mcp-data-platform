# MCP Apps Development

Create and test MCP Apps using the included development environment.

## Prerequisites

- Docker (only dependency needed)

## Quick Start

```bash
git clone https://github.com/txn2/mcp-data-platform
cd mcp-data-platform

docker compose -f docker-compose.dev.yml up
```

This starts:

| Component | URL | Purpose |
|-----------|-----|---------|
| Test Harness | http://localhost:8000 | App development and testing |
| MCP Server | http://localhost:3001 | MCP protocol (SSE) |
| MCP Inspector | http://localhost:6274 | Protocol debugging |
| Trino | http://localhost:8090 | Query engine |

## Development Workflow

### 1. Open Test Harness

Open http://localhost:8000/test-harness.html

The test harness provides:

- **Left panel**: Editable JSON test data
- **Right panel**: Live app preview
- **Send Test Data**: Sends data to the app
- **Reload App**: Reloads after editing source files

### 2. Edit and Test

1. Edit `./apps/platform-info/index.html` (or `./apps/prompt-browser/index.html` / `./apps/query-results/index.html` for those apps)
2. Click **Reload App** in the test harness
3. Click **Send Test Data**
4. See changes immediately

Changes are served instantly — no restart needed. The dev config (`configs/mcpapps-dev.yaml`) uses `assets_path` to point at the local source directory, overriding the embedded HTML so edits are reflected without rebuilding the binary.

### 3. Test with Real Queries

To test with actual Trino queries:

1. Open http://localhost:6274 (MCP Inspector)
2. Connect to: `http://mcp-server:3001/sse`
3. Run `trino_query`:
   ```json
   {"sql": "SELECT 1 as id, 'Test' as name, 100.50 as value"}
   ```

## Creating New Apps

### App Structure

```
my-app/
├── index.html    # Entry point (required)
├── styles.css    # Optional
├── app.js        # Optional
└── assets/       # Optional
```

### MCP Apps Protocol

Apps communicate with the host via `postMessage`:

```javascript
// Initialize on load
window.parent.postMessage({
  jsonrpc: '2.0',
  id: 1,
  method: 'ui/initialize',
  params: {
    protocolVersion: '2025-01-09',
    appInfo: { name: 'My App', version: '1.0.0' }
  }
}, '*');

// Listen for tool results
window.addEventListener('message', (event) => {
  if (event.data?.method === 'ui/notifications/tool-result') {
    const data = JSON.parse(event.data.params.content[0].text);
    // Render your UI
  }
});
```

### Calling Tools from an App

An app is not limited to the result that rendered it. It can call tools itself with `tools/call`, or `ui/call-tool` on hosts speaking protocol `2025-01-09`. Tool visibility defaults to `["model", "app"]`, so every tool this server exposes is app-callable.

Those calls reach the server over the same MCP transport as the agent's and are gated the same way. When the session handle requirement is on, the platform refuses any tool call that does not carry a `session_id` minted by `platform_info`:

```
SESSION_REQUIRED: Call platform_info first. It returns a session_id you must
pass as the session_id argument on every subsequent tool call.
```

An app that calls any tool other than `platform_info` must therefore handshake first:

1. Call `platform_info` with empty arguments before the first data call. The result's first text content block is JSON carrying `session_id`.
2. Thread that value as the `session_id` argument on every subsequent call. Do it in whatever wrapper issues the call, so no call site can forget it.
3. On a rejection whose message contains `SESSION_REQUIRED`, `SESSION_EXPIRED`, or `SETUP_REQUIRED`, discard the stored handle, call `platform_info` again, and replay the call once. Handles expire on inactivity, so a long-lived app will hit this. `SETUP_REQUIRED` comes from the transport-keyed session gate a deployment may run instead of explicit handles; its remedy is the same call.
4. Treat a handshake result with no `session_id` as "handles are not enabled in this deployment" and send no `session_id` argument. Threading nothing is correct there.

`platform_info` is never gated, because it is the tool that mints the handle. The handshake itself cannot be refused for want of a handle.

Do not make the handshake fatal. A deployment may have handles off, and a persona may allow your app's tool while denying `platform_info`, so a failed handshake should still let the first data call run and report whatever the platform actually says. Blanking the app on a `platform_info` failure breaks cases that worked before the handshake existed.

`apps/prompt-browser/index.html` implements this. Its `handshake`, `withSession`, and `callTool` functions are the smallest complete version.

One correlation hazard: hosts that deliver results as `ui/notifications/tool-result` notifications send no request id, so an app that matches responses by payload shape must be able to tell the `platform_info` result from its own tool's result. `config_version` and `features` are unconditional on the `platform_info` payload and identify it. Do not key on `prompts`: `platform_info` also carries a `prompts` array listing the server's registered MCP prompts.

The test harness enforces this gate. It answers a `platform_info` call from any app other than `platform-info` with a handshake payload carrying a freshly minted `session_id`, and refuses any other tool call whose `session_id` is missing or stale with the same `SESSION_REQUIRED` / `SESSION_EXPIRED` text the platform returns. An app that skips the handshake therefore fails in development rather than only in a gated deployment. The **Expire Session** button revokes the live handle so the next call is refused, which is how you exercise the recovery path without waiting for a real expiry.

### Minimal Example

```html
<!DOCTYPE html>
<html>
<head><title>My App</title></head>
<body>
  <div id="results"></div>
  <script>
    window.addEventListener('message', (event) => {
      if (event.data?.method === 'ui/notifications/tool-result') {
        const data = JSON.parse(event.data.params.content[0].text);
        document.getElementById('results').innerHTML =
          `<pre>${JSON.stringify(data, null, 2)}</pre>`;
      }
    });

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

### Add to Development Environment

1. Create your app directory under `apps/`:
   ```
   apps/
   ├── platform-info/
   ├── prompt-browser/
   ├── query-results/
   └── my-new-app/
       └── index.html
   ```

2. Add configuration to `configs/mcpapps-dev.yaml`:
   ```yaml
   mcpapps:
     apps:
       my_new_app:
         enabled: true
         assets_path: "/apps/my-new-app"
         tools:
           - some_tool
   ```

3. Restart the dev environment:
   ```bash
   docker compose -f docker-compose.dev.yml up
   ```

## Test Queries

All queries use Trino's memory catalog (no database setup needed).

**Simple data:**
```json
{"sql": "SELECT 1 as id, 'Product A' as name, 15000.50 as revenue UNION ALL SELECT 2, 'Product B', 23000.75 UNION ALL SELECT 3, 'Product C', 8500.25"}
```

**More rows:**
```json
{"sql": "SELECT n as id, 'Item ' || CAST(n AS VARCHAR) as name, ROUND(RANDOM() * 10000, 2) as value FROM UNNEST(SEQUENCE(1, 20)) AS t(n)"}
```

**Date series:**
```json
{"sql": "SELECT DATE '2024-01-01' + INTERVAL '1' DAY * n as date, ROUND(RANDOM() * 1000, 2) as sales FROM UNNEST(SEQUENCE(0, 29)) AS t(n)"}
```

## Architecture

```mermaid
graph LR
    subgraph "Browser"
        TH[Test Harness<br/>:8000]
        MI[MCP Inspector<br/>:6274]
    end

    subgraph "Docker"
        MCP[MCP Server<br/>:3001]
        T[Trino<br/>:8090]
    end

    TH <-->|postMessage| MCP
    MI <-->|MCP Protocol| MCP
    MCP <-->|SQL| T
```

## Debugging

1. Open browser DevTools (F12)
2. Console tab shows `[MCP-APP]` prefixed logs
3. Network tab shows MCP protocol messages

## Troubleshooting

| Problem | Solution |
|---------|----------|
| Changes not appearing | Click Reload App, or hard refresh (Cmd+Shift+R) |
| "No results to display" | Check browser console for errors |
| App shows `SESSION_REQUIRED` | The app called a tool before handshaking; see [Calling Tools from an App](#calling-tools-from-an-app) |
| Trino not responding | Wait for startup: `curl http://localhost:8090/v1/info` |
| Port already in use | `docker compose -f docker-compose.dev.yml down` |

## Cleanup

```bash
docker compose -f docker-compose.dev.yml down
```
