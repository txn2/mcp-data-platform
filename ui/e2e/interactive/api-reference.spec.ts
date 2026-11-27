import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";
import { contrastFailures, report } from "./helpers/contrast";

// The platform's own REST surface, read in the portal (#1742).
//
// The unit tests decide which document the page points ReDoc at and which
// palette it hands over. Only the assembled app can show the thing the ticket
// is about: that an administrator can find the reference at all, and that what
// opens is the document rendered rather than an empty frame or a failure.
//
// The document here is the mock server's (src/mocks/data/platformSpec.ts), a
// slice copied out of the one the platform serves -- Swagger 2.0, the same tag
// names, the same gateway routes. What that leaves unproven is the bytes a
// running server transmits, which is the server's own test to keep.

const REFERENCE_PATH = "/portal/admin/api-reference";

/**
 * GATEWAY_PATH is the Gateway tag's own address. ReDoc 3 gives every section,
 * tag and operation an address under the page and draws a tag's operations
 * once the reader reaches it (#2769), so the tests open the tag they read.
 */
const GATEWAY_PATH = `${REFERENCE_PATH}/gateway`;

/** TAG_HEADING is the Gateway tag's section heading in the rendered document. */
const TAG_HEADING = '.redoc-host h2:text-is("Gateway")';

/** INVOKE_ENTRY is the menu entry of the gateway's invoke operation. */
const INVOKE_ENTRY =
  '.redoc-host .menu-content a[href$="/gateway/paths/~1gateway~1{connection}~1invoke/post"]';

/** railOrder is the rail's rows in the order a reader scans them: each section
 * caption and each nav button, as one flat list of labels. */
async function railOrder(page: Page): Promise<string[]> {
  return page
    .locator("nav")
    .first()
    .evaluate((nav) =>
      Array.from(nav.children).map((el) => el.textContent?.trim() ?? ""),
    );
}

/** setTheme selects a theme the way the portal's own toggle does, and opens
 * the Gateway tag again: an expanded disclosure can move ReDoc 3 to the
 * address of the operation it belongs to, where the tag's heading is not
 * drawn. */
async function setTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  await page.evaluate(
    (t) => localStorage.setItem("mcp-portal-theme", t),
    theme,
  );
  await page.goto(GATEWAY_PATH);
  await expect(page.locator(TAG_HEADING)).toBeVisible({ timeout: 30_000 });
}

/** isPlatformOrigin reports whether a request goes to the server the suite
 * runs against, or never leaves the page (data:, blob:). */
function isPlatformOrigin(url: string, base: string): boolean {
  const u = new URL(url);
  if (u.protocol === "data:" || u.protocol === "blob:") return true;
  return u.origin === new URL(base).origin;
}

test.describe("The API reference an administrator reaches from the portal", () => {
  test("is a row of the admin rail, and opens on the reference", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto("/portal/admin");

    const order = await railOrder(page);
    const user = order.indexOf("User");
    const admin = order.indexOf("Admin");
    const item = order.indexOf("API Reference");
    expect(admin, "the rail has an Admin section").toBeGreaterThan(user);
    // Under the Admin caption, not the User one: the reference is reached from
    // the administrator's section of the rail.
    expect(item, "API Reference sits in the Admin section").toBeGreaterThan(
      admin,
    );

    await page.getByRole("button", { name: "API Reference" }).click();

    await expect(page).toHaveURL(new RegExp(`${REFERENCE_PATH}$`));
    await expect(
      page.getByRole("heading", { name: "API Reference", level: 1 }),
    ).toBeVisible();
  });

  test("renders the served document, not an empty frame", async ({ page }) => {
    await authenticate(page);
    await page.goto(GATEWAY_PATH);

    // The tag the gateway routes are filed under, and the operation a non-MCP
    // caller invokes a connection through. Both come from the document: if the
    // fetch failed, the conversion failed, or ReDoc mounted over nothing, the
    // page still has its chrome and none of this.
    await expect(page.locator(TAG_HEADING)).toBeVisible({ timeout: 30_000 });
    await expect(
      page.getByRole("heading", {
        name: /Call an upstream connection through the API gateway$/,
      }),
    ).toBeVisible();
    // Exactly the invoke operation: the document also carries /invoke-raw,
    // whose address this one is a prefix of.
    await expect(page.locator(INVOKE_ENTRY)).toBeVisible();
  });

  test("keeps the reader in the portal as they move through it", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto(REFERENCE_PATH);

    // A menu entry is an address under the page, not one from the site's root:
    // ReDoc 3 writes its links from "/" unless given a base path, and "Gateway"
    // would have opened /gateway, outside the portal.
    const gateway = page.locator(
      '.redoc-host .menu-content a[href="/portal/admin/api-reference/gateway"]',
    );
    await expect(gateway).toHaveText("Gateway", { timeout: 30_000 });
    await gateway.click();
    await expect(page).toHaveURL(new RegExp(`${GATEWAY_PATH}$`));
    await expect(page.locator(TAG_HEADING)).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "API Reference", level: 1 }),
    ).toBeVisible();

    // The address is one the portal renders when opened directly, so it can be
    // bookmarked and shared, and Back returns to where the reader was.
    await page.reload();
    await expect(page.locator(TAG_HEADING)).toBeVisible({ timeout: 30_000 });
    await page.goBack();
    await expect(page).toHaveURL(new RegExp(`${REFERENCE_PATH}$`));
    await expect(
      page.getByRole("heading", { name: "API Reference", level: 1 }),
    ).toBeVisible();
  });

  test("calls nothing outside the platform, and offers one theme switch", async ({
    page,
    baseURL,
  }) => {
    const outside: string[] = [];
    page.on("request", (r) => {
      if (!isPlatformOrigin(r.url(), baseURL!)) outside.push(r.url());
    });
    await authenticate(page);
    await page.goto(GATEWAY_PATH);
    await expect(page.locator(TAG_HEADING)).toBeVisible({ timeout: 30_000 });
    await page.locator(INVOKE_ENTRY).click();
    await page.waitForLoadState("networkidle");

    // ReDoc 3 draws a vendor logo from Redocly's CDN and sends telemetry to
    // Redocly's collector by default (#2769). An air-gapped deployment has no
    // route to either, and no other should tell a vendor when its
    // administrators read the admin surface.
    expect(outside, "requests that left the platform's origin").toEqual([]);

    // The portal's header switch is the one that sets the theme. ReDoc's own
    // writes a key of its own that would outrank it for this page alone.
    await expect(page.getByTestId("color-mode-switcher")).toBeHidden();
    await expect(page.locator('.redoc-host a[href="https://redocly.com/redoc/"]')).toBeHidden();
  });

  test("offers the interactive reader beside it", async ({ page }) => {
    await authenticate(page);
    await page.goto(REFERENCE_PATH);

    // Asserted by where it resolves rather than by following it: Swagger UI is
    // served by the platform, outside the SPA, and the mock server the suite
    // runs against does not serve that page.
    const link = page.getByRole("link", { name: /Swagger UI/ });
    await expect(link).toHaveAttribute("href", "/api/v1/admin/docs/index.html");
    await expect(link).toHaveAttribute("target", "_blank");
  });

  test("meets AA contrast on every element of the document, both themes", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto(GATEWAY_PATH);
    await expect(page.locator(TAG_HEADING)).toBeVisible({ timeout: 30_000 });

    for (const theme of ["light", "dark"] as const) {
      await setTheme(page, theme);
      // Every disclosure open, so the sweep reaches what a reader expands
      // rather than only what the page paints first: each response block, the
      // security requirement, the nested schemas and the sample tabs. Only
      // disclosures: in ReDoc 3 the menu, an operation's summary row and the
      // sidebar's own controls are buttons too, and clicking them moves the
      // reader to another address or folds the menu away.
      await page.evaluate(() => {
        const moves = /^(get|put|post|patch|delete|head|options)\s*\//i;
        const chrome = new Set([
          "Hide sidebar",
          "Show sidebar",
          "Open navigation menu",
          "More actions",
          "Copy",
        ]);
        document
          .querySelectorAll<HTMLElement>(
            '.redoc-host [role="button"], .redoc-host button',
          )
          .forEach((el) => {
            if (el.closest(".menu-content")) return;
            const label = (el.getAttribute("aria-label") ?? el.textContent ?? "").trim();
            if (chrome.has(label) || moves.test(label)) return;
            el.click();
          });
      });
      await page.waitForTimeout(500);

      const failures = await contrastFailures(page);
      expect(
        failures,
        `${theme} theme: ${failures.length} element(s) below AA\n${report(failures)}`,
      ).toEqual([]);
    }
  });

  test("reaches its last navigation entry at every window height", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto(GATEWAY_PATH);
    await expect(page.locator(TAG_HEADING)).toBeVisible({ timeout: 30_000 });

    // Short enough that ReDoc's menu overflows whatever it is given.
    await page.setViewportSize({ width: 1440, height: 400 });
    await page.waitForTimeout(300);

    const geometry = await page.evaluate(() => {
      const menu = document.querySelector<HTMLElement>(
        ".redoc-host .menu-content",
      );
      const scrollport = document.querySelector<HTMLElement>("main");
      if (!menu || !scrollport) return null;
      // ReDoc's menu is a fixed-height box holding a search field and, below
      // it, the box that actually scrolls. Found by its overflow rather than by
      // a class, which is a styled-components hash.
      const scroller = Array.from(
        menu.querySelectorAll<HTMLElement>("div"),
      ).find((el) => getComputedStyle(el).overflowY === "auto");
      if (!scroller) return null;
      // To the end of the list, which is what a reader looking for the last
      // entry does.
      scroller.scrollTop = scroller.scrollHeight;
      const items = menu.querySelectorAll("li");
      const last = items[items.length - 1];
      return {
        entries: items.length,
        menuHeight: menu.getBoundingClientRect().height,
        listHeight: scroller.scrollHeight,
        scrollportHeight: scrollport.clientHeight,
        scrollportBottom: scrollport.getBoundingClientRect().bottom,
        lastEntryBottom: last ? last.getBoundingClientRect().bottom : 0,
      };
    });
    expect(
      geometry,
      "the menu, its scrolling list and the page's scroll container are all present",
    ).not.toBeNull();
    const g = geometry!;
    expect(g.entries, "the menu has entries to reach").toBeGreaterThan(0);
    expect(
      g.listHeight,
      "the window is short enough that the entries do not all fit, which is the case under test",
    ).toBeGreaterThan(g.scrollportHeight);

    // ReDoc sets the sticky menu's height inline to `calc(top + 100vh)`, but
    // the portal's scroll container is AppShell's <main>, which is the viewport
    // less the fixed header. The difference is the header's height, and the
    // entries that landed in it could not be scrolled to by any means (#1749).
    expect(
      Math.round(g.menuHeight),
      "the menu is no taller than the container it scrolls inside",
    ).toBeLessThanOrEqual(Math.round(g.scrollportHeight));

    // Scrolled to its end, the last entry is on screen.
    expect(
      Math.round(g.lastEntryBottom),
      "the last navigation entry is reachable",
    ).toBeLessThanOrEqual(Math.round(g.scrollportBottom));
  });
});

test.describe("The pointer from the operation browser", () => {
  test("opens the reference on the gateway routes", async ({ page }) => {
    await authenticate(page);
    await page.goto(
      "/portal/apis?connection=acme-billing&spec=core&op=createCustomer",
    );

    const link = page.getByRole("link", {
      name: "Platform REST reference (auth, gateway routes, status codes)",
    });
    // The fragment is the one the served Swagger UI mints for a tag, so the
    // reader lands on the gateway routes rather than at the top of a document
    // that describes a few hundred paths.
    await expect(link).toHaveAttribute(
      "href",
      "/api/v1/admin/docs/index.html#/Gateway",
    );
  });
});
