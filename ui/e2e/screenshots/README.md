# Portal Screenshots

Automated screenshot generation for every portal page in light and dark modes.

## Quick Start

```bash
cd ui
pnpm screenshots          # Generate PNGs to public/images/screenshots/
pnpm screenshots:convert  # Convert PNGs to WebP (removes PNGs)
```

Both steps together:

```bash
pnpm screenshots && pnpm screenshots:convert
```

Screenshots output to `docs/images/screenshots/{light,dark}/`.

## Custom Output Directory

```bash
SCREENSHOT_OUTPUT_DIR=/path/to/output pnpm screenshots
SCREENSHOT_OUTPUT_DIR=/path/to/output pnpm screenshots:convert
```

## Custom Branding

Create a JSON file and point to it:

```bash
SCREENSHOT_BRANDING_FILE=/path/to/branding.json \
SCREENSHOT_PREFIX=plexara- \
SCREENSHOT_OUTPUT_DIR=/path/to/website/public/images/screenshots \
  pnpm screenshots
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
3. Run `pnpm test` to verify the route sync test passes
4. Run `pnpm screenshots` to generate

## File Structure

```
e2e/screenshots/
  playwright.config.ts    # Playwright config (starts Vite+MSW dev server)
  screenshot.spec.ts      # Main test that drives all screenshots
  route-manifest.ts       # All routes, tabs, parameterized IDs
  branding.config.ts      # Branding config loader
  route-sync.test.ts      # Validates manifest matches AppShell routes
  helpers/
    auth.ts               # Login helper
    theme.ts              # Light/dark toggle
    wait.ts               # Wait-for-idle helpers
    convert.ts            # PNG-to-WebP conversion
```
