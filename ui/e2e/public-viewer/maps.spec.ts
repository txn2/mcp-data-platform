import { test, expect, type Frame, type Page } from "@playwright/test";

// Street maps (#2068) through a public share: a route map drawn on the
// basemap and runtime the platform serves, and a state choropleth that needs
// no basemap. The dev seed puts the San Francisco region under
// maps/uploads/ and shares both maps publicly, so these run against the live
// stack with no fetch and no internet.

const ROUTE_TOKEN = "tok-route-map-public";
const CHOROPLETH_TOKEN = "tok-choropleth-public";

/** Console messages reporting something the page's policy refused. */
function watchRefusals(page: Page): string[] {
  const refusals: string[] = [];
  const refused = (text: string) => /Content Security Policy|Refused to (load|connect|execute|create)/i.test(text);
  page.on("console", (m) => {
    if (refused(m.text())) refusals.push(m.text());
  });
  page.on("pageerror", (e) => {
    if (refused(e.message)) refusals.push(e.message);
  });
  return refusals;
}

/** Requests the page made to any host but its own. */
function watchForeignHosts(page: Page, ownHost: string): string[] {
  const foreign: string[] = [];
  page.on("request", (r) => {
    const u = new URL(r.url());
    if ((u.protocol === "http:" || u.protocol === "https:") && u.host !== ownHost && !foreign.includes(u.host)) {
      foreign.push(u.host);
    }
  });
  return foreign;
}

/** The share's artifact frame, once it exists. */
async function artifact(page: Page): Promise<Frame> {
  await expect(page.locator("iframe").first()).toBeAttached();
  const handle = await page.locator("iframe").first().elementHandle();
  const frame = await handle!.contentFrame();
  return frame!;
}

/**
 * Waits for the map to finish drawing: its style, the basemap tiles in view,
 * and the route the document added once the style loaded.
 */
async function mapDrawn(frame: Frame) {
  // The document declares the map as a top-level `const map`, a global
  // binding but not a property of window (window.map is the #map element),
  // so it is read by a predicate evaluated in the frame's global scope.
  await frame.waitForFunction(
    `typeof map === "object" && typeof map.loaded === "function" &&
     map.loaded() && map.areTilesLoaded() && !!map.getLayer("route")`,
    undefined,
    { timeout: 30_000 },
  );
}

test.describe("a map share", () => {
  test("draws the route over the served basemap, from this origin only", async ({ page, baseURL }) => {
    const refusals = watchRefusals(page);
    const foreign = watchForeignHosts(page, new URL(baseURL!).host);
    const archiveReads: number[] = [];
    page.on("response", (r) => {
      if (new URL(r.url()).pathname === "/portal/maps/san-francisco.pmtiles") archiveReads.push(r.status());
    });

    await page.goto(`/portal/view/${ROUTE_TOKEN}`, { waitUntil: "networkidle" });
    const frame = await artifact(page);
    await mapDrawn(frame);

    // The basemap was read by range, and its labels and attribution drew.
    expect(archiveReads.length).toBeGreaterThan(0);
    expect(archiveReads.every((s) => s === 206), `archive reads: ${archiveReads.join(", ")}`).toBe(true);
    await expect(frame.locator(".maplibregl-ctrl-attrib")).toContainText("OpenStreetMap");
    await expect(frame.locator("canvas.maplibregl-canvas")).toBeVisible();

    expect(refusals, refusals.join("\n")).toHaveLength(0);
    expect(foreign, `fetched from ${foreign.join(", ")}`).toHaveLength(0);
  });

  test("still draws with every other host unreachable", async ({ page, baseURL }) => {
    const own = new URL(baseURL!).host;
    await page.route("**/*", (route) => {
      const u = new URL(route.request().url());
      if (u.protocol.startsWith("http") && u.host !== own) return route.abort("internetdisconnected");
      return route.continue();
    });
    await page.goto(`/portal/view/${ROUTE_TOKEN}`, { waitUntil: "networkidle" });
    await mapDrawn(await artifact(page));
  });
});

test.describe("a choropleth share", () => {
  test("draws every state from the served boundaries", async ({ page, baseURL }) => {
    const refusals = watchRefusals(page);
    const foreign = watchForeignHosts(page, new URL(baseURL!).host);
    await page.goto(`/portal/view/${CHOROPLETH_TOKEN}`, { waitUntil: "networkidle" });
    const frame = await artifact(page);
    // The projected states object holds the 50 states and DC.
    await expect(frame.locator("svg#map path.state")).toHaveCount(51);
    await expect(frame.locator("#legend span")).toHaveCount(4);
    expect(refusals, refusals.join("\n")).toHaveLength(0);
    expect(foreign, `fetched from ${foreign.join(", ")}`).toHaveLength(0);
  });
});
