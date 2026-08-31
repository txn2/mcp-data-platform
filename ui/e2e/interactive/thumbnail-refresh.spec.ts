import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// An asset a managed script rewrites is the one nobody is looking at when it
// changes, so a capture that only happens on the page the asset is listed on
// never happens at all (#1431). The unit tests decide what the queue does with
// a work list and the real-Postgres tests decide what is on it; only the
// assembled app shows that a browser asks for that list, and acts on it, from a
// page with no assets on it.

test.describe("Thumbnail refresh", () => {
  test("captures a rewritten asset from a page that lists no assets", async ({ page }) => {
    const asked: string[] = [];
    const fetched: string[] = [];
    const uploaded: string[] = [];
    page.on("request", (r) => {
      const url = r.url();
      if (url.includes("/thumbnails/pending")) asked.push(url);
      if (r.method() === "GET" && /\/assets\/[^/]+\/content/.test(url)) fetched.push(url);
      // Assets only. A managed resource is captured by the same queue in the
      // same tab, so "the first upload" is not necessarily the one this test is
      // about (#1568).
      if (r.method() === "PUT" && /\/assets\/[^/]+\/thumbnail/.test(url)) uploaded.push(url);
    });

    // The fixture library is settled, so nothing in the portal is pending by
    // default and no other spec pays for a capture it is not testing. This one
    // is testing it: ast-002 (an SVG, one capture rather than two) is marked a
    // version behind before the app boots, which is the state a rewrite leaves.
    await page.addInitScript(() => {
      (globalThis as { __STALE_THUMBNAILS__?: string[] }).__STALE_THUMBNAILS__ = ["ast-002"];
    });

    await authenticate(page);
    // A page with no asset on it at all.
    await page.goto("/portal/settings");
    await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();

    await expect.poll(() => asked.length, { timeout: 20_000 }).toBeGreaterThan(0);
    await expect
      .poll(() => fetched.filter((u) => u.includes("ast-002")).length, { timeout: 20_000 })
      .toBeGreaterThan(0);
    await expect.poll(() => uploaded.length, { timeout: 20_000 }).toBeGreaterThan(0);
    // Dated to the version it was rendered from, so the server can tell a
    // capture that has caught up from one that has not.
    expect(uploaded[0]).toContain("version=3");
  });

  // A referencing artifact was captured with every reference blocked: the
  // capture frame carried a hand-written copy of the renderer's policy that
  // granted the reference route to nothing, so the artifact drew its own
  // failure branch and that is what was rasterized and stored (#1497). Only a
  // real browser can say the policy now permits the load, because only a
  // browser enforces it.
  test("captures a referencing artifact with its references resolved", async ({ page }) => {
    const uploaded: string[] = [];
    const refs: { url: string; status: number }[] = [];
    page.on("request", (r) => {
      if (r.method() === "PUT" && r.url().includes("/thumbnail")) uploaded.push(r.url());
    });
    page.on("response", (r) => {
      if (r.url().includes("/portal/refs/")) refs.push({ url: r.url(), status: r.status() });
    });

    // The capture fixtures are added only for the specs that capture them, so
    // no screenshot and no list-page spec is taken against a library holding
    // two assets that exist to be rasterized.
    await page.addInitScript(() => {
      const g = globalThis as { __STALE_THUMBNAILS__?: string[]; __REF_CAPTURE_ASSETS__?: boolean };
      g.__REF_CAPTURE_ASSETS__ = true;
      g.__STALE_THUMBNAILS__ = ["ast-011"];
    });

    await authenticate(page);
    await page.goto("/portal/settings");
    await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();

    await expect
      .poll(() => uploaded.filter((u) => u.includes("ast-011")).length, { timeout: 40_000 })
      .toBeGreaterThan(0);

    // The image and the data file both came from the reference route, and both
    // answered. A capture stored without them would be a picture of the error
    // branch, which is the defect.
    const forAsset = refs.filter((r) => r.url.includes("/portal/refs/ast-011/"));
    expect(forAsset.length).toBeGreaterThanOrEqual(2);
    expect(forAsset.every((r) => r.status === 200)).toBe(true);
  });

  // The other half: a capture the frame reported a failed reference load for is
  // thrown away, and the asset is left for another try rather than keeping a
  // picture of its own error state.
  test("stores nothing for an artifact whose reference did not load", async ({ page }) => {
    const uploaded: string[] = [];
    const fetched: string[] = [];
    page.on("request", (r) => {
      const url = r.url();
      // Assets only. A managed resource is captured by the same queue in the
      // same tab, so "the first upload" is not necessarily the one this test is
      // about (#1568).
      if (r.method() === "PUT" && /\/assets\/[^/]+\/thumbnail/.test(url)) uploaded.push(url);
      if (r.method() === "GET" && /\/assets\/[^/]+\/content/.test(url)) fetched.push(url);
    });

    await page.addInitScript(() => {
      const g = globalThis as { __STALE_THUMBNAILS__?: string[]; __REF_CAPTURE_ASSETS__?: boolean };
      g.__REF_CAPTURE_ASSETS__ = true;
      g.__STALE_THUMBNAILS__ = ["ast-012"];
    });

    await authenticate(page);
    await page.goto("/portal/settings");
    await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();

    // The queue did reach this asset -- it read its content -- so the absence
    // of an upload below is the capture being discarded, not the capture never
    // being attempted.
    await expect
      .poll(() => fetched.filter((u) => u.includes("ast-012")).length, { timeout: 40_000 })
      .toBeGreaterThan(0);

    await page.waitForTimeout(15_000);
    expect(uploaded.filter((u) => u.includes("ast-012"))).toHaveLength(0);
  });

  // The JSON families were skipped before a capture ever started: neither
  // capture site recognized them, so a JSON asset kept the placeholder icon
  // (#1432). Both are drawn on the platform's own background, so each is
  // captured twice -- once per color scheme -- and both variants are uploaded.
  test("captures both color schemes for the JSON families", async ({ page }) => {
    const uploaded: string[] = [];
    page.on("request", (r) => {
      const url = r.url();
      // Assets only. A managed resource is captured by the same queue in the
      // same tab, so "the first upload" is not necessarily the one this test is
      // about (#1568).
      if (r.method() === "PUT" && /\/assets\/[^/]+\/thumbnail/.test(url)) uploaded.push(url);
    });

    await page.addInitScript(() => {
      (globalThis as { __STALE_THUMBNAILS__?: string[] }).__STALE_THUMBNAILS__ = ["ast-009", "ast-010"];
    });

    await authenticate(page);
    await page.goto("/portal/settings");
    await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();

    for (const id of ["ast-009", "ast-010"]) {
      await expect
        .poll(() => uploaded.filter((u) => u.includes(id) && !u.includes("variant=dark")).length, {
          timeout: 30_000,
        })
        .toBeGreaterThan(0);
      await expect
        .poll(() => uploaded.filter((u) => u.includes(id) && u.includes("variant=dark")).length, {
          timeout: 30_000,
        })
        .toBeGreaterThan(0);
    }
  });
});
