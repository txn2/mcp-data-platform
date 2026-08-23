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
      if (r.method() === "PUT" && url.includes("/thumbnail")) uploaded.push(url);
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

  // The JSON families were skipped before a capture ever started: neither
  // capture site recognized them, so a JSON asset kept the placeholder icon
  // (#1432). Both are drawn on the platform's own background, so each is
  // captured twice -- once per color scheme -- and both variants are uploaded.
  test("captures both color schemes for the JSON families", async ({ page }) => {
    const uploaded: string[] = [];
    page.on("request", (r) => {
      const url = r.url();
      if (r.method() === "PUT" && url.includes("/thumbnail")) uploaded.push(url);
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
