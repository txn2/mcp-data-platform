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
 * TAG_HEADING is the Gateway tag's section heading in the rendered document.
 *
 * Addressed through the self-link ReDoc puts inside it rather than by text:
 * the heading's accessible name is that link's target followed by the tag name
 * ("tag/Gateway Gateway"), so a name match would be matching ReDoc's anchor
 * scheme by accident. This matches it on purpose.
 */
const TAG_HEADING = 'h2:has(a[href="#tag/Gateway"])';

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

/** setTheme selects a theme the way the portal's own toggle does, and reloads:
 * ReDoc resolves its theme once, when the document mounts. */
async function setTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  await page.evaluate(
    (t) => localStorage.setItem("mcp-portal-theme", t),
    theme,
  );
  await page.reload();
  await expect(page.locator(TAG_HEADING)).toBeVisible({ timeout: 30_000 });
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
    await page.goto(REFERENCE_PATH);

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
    await expect(
      // Exactly: the document also carries /invoke-raw, whose button name this
      // one is a prefix of.
      page.getByRole("button", {
        name: "post /gateway/{connection}/invoke",
        exact: true,
      }),
    ).toBeVisible();
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
    await page.goto(REFERENCE_PATH);
    await expect(page.locator(TAG_HEADING)).toBeVisible({ timeout: 30_000 });

    for (const theme of ["light", "dark"] as const) {
      await setTheme(page, theme);
      // Every disclosure open, so the sweep reaches what a reader expands
      // rather than only what the page paints first: each response block, the
      // security requirement, the nested schemas and the sample tabs.
      await page.evaluate(() => {
        document
          .querySelectorAll<HTMLElement>(
            '.redoc-host [role="button"], .redoc-host button',
          )
          .forEach((el) => el.click());
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
    await page.goto(REFERENCE_PATH);
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
