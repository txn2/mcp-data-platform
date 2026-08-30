# Portal Screenshots

Automated screenshot generation for every portal page in light and dark modes.

## Quick Start

```bash
cd ui
npm run screenshots          # Generate portal PNGs to docs/images/screenshots/
npm run screenshots:apps     # Generate MCP App PNGs to the same place
npm run screenshots:convert  # Convert PNGs to WebP (removes PNGs)
```

Both steps together:

```bash
npm run screenshots && npm run screenshots:apps && npm run screenshots:convert
```

Screenshots output to `docs/images/screenshots/{light,dark}/`.

## Custom Output Directory

```bash
SCREENSHOT_OUTPUT_DIR=/path/to/output npm run screenshots
SCREENSHOT_OUTPUT_DIR=/path/to/output npm run screenshots:convert
```

## Custom Branding

Create a JSON file and point to it:

```bash
SCREENSHOT_BRANDING_FILE=/path/to/branding.json \
SCREENSHOT_PREFIX=acme- \
SCREENSHOT_OUTPUT_DIR=/path/to/website/public/images/screenshots \
  npm run screenshots
```

branding.json:
```json
{
  "platformName": "ACME Corp Data Platform",
  "portalTitle": "My Platform"
}
```

## Adding New Routes

1. Add the route to `route-manifest.ts`
2. If the route needs mock data, add MSW handlers in `src/mocks/handlers.ts`
3. Run `npm test` to verify the route sync test passes
4. Run `npm run screenshots` to generate

## File Structure

```
e2e/screenshots/
  playwright.config.ts    # Portal config (starts Vite+MSW dev server)
  screenshot.spec.ts      # Main test that drives all portal screenshots
  route-manifest.ts       # All routes, tabs, parameterized IDs
  branding.config.ts      # Branding config loader
  route-sync.test.ts      # Validates manifest matches AppShell routes
  mcpapp.config.ts        # MCP Apps config (no dev server; see below)
  mcpapp.spec.ts          # Host bridge + capture for the MCP Apps
  mcpapp-manifest.ts      # The apps, their tool result fixtures, their tabs
  helpers/
    auth.ts               # Login helper
    theme.ts              # Light/dark toggle
    wait.ts               # Wait-for-idle helpers
    convert.ts            # PNG-to-WebP conversion
```

## MCP Apps

An MCP App is a self-contained HTML document the platform serves as a UI
resource; a client renders it in an iframe and talks to it over postMessage.
Capturing one therefore needs no platform, no sign-in and no MSW: `mcpapp.spec.ts`
plays the host itself, which is why it has its own config with no `webServer`.

It does what the server and a client do, in that order:

1. replaces the app's `<script id="app-config">` with the deployment config,
   the same substitution `internal/platform/mcpapps/resource.go` makes at
   serve time;
2. answers `ui/initialize`, answers `ui/call-tool`, and pushes the fixture as
   a `ui/notifications/tool-result`;
3. honours `ui/notifications/size-changed`, so each app is captured at the
   height it asks for rather than at a fixed guess.

The apps have no in-page theme control -- they read `prefers-color-scheme` --
so each theme gets its own browser context. A capture that produced a blank
panel would be a passing test with an empty image, so the spec asserts the
rendered app has text before it writes the file.

To add an app or another of its tabs, edit `mcpapp-manifest.ts`: a `views`
entry is a selector clicked inside the iframe after the result lands, captured
as `app-<slug>-<suffix>-<theme>`.
