import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

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

/** luminance is the perceived lightness (0-1) of a CSS color. */
function luminance(css: string): number {
  const [r, g, b] = css.match(/\d+(\.\d+)?/g)!.map(Number) as [number, number, number];
  return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
}

/** textLuminance is the lightness of an element's text color, which is how
 * these tests ask "is this readable on that ground" without pinning a hex the
 * palette is free to adjust. */
async function textLuminance(page: Page, selector: string): Promise<number> {
  const color = await page
    .locator(selector)
    .first()
    .evaluate((el) => getComputedStyle(el).color);
  return luminance(color);
}

/** inkGap is how far an element's text sits from its own background. */
async function inkGap(page: Page, selector: string): Promise<number> {
  const pair = await page
    .locator(selector)
    .first()
    .evaluate((el) => {
      const s = getComputedStyle(el);
      return { color: s.color, background: s.backgroundColor };
    });
  return Math.abs(luminance(pair.color) - luminance(pair.background));
}

test.describe("The API reference an administrator reaches from the portal", () => {
  test("is a row of the admin rail, and opens on the reference", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/admin");

    const order = await railOrder(page);
    const user = order.indexOf("User");
    const admin = order.indexOf("Admin");
    const item = order.indexOf("API Reference");
    expect(admin, "the rail has an Admin section").toBeGreaterThan(user);
    // Under the Admin caption, not the User one: the reference is reached from
    // the administrator's section of the rail.
    expect(item, "API Reference sits in the Admin section").toBeGreaterThan(admin);

    await page.getByRole("button", { name: "API Reference" }).click();

    await expect(page).toHaveURL(new RegExp(`${REFERENCE_PATH}$`));
    await expect(page.getByRole("heading", { name: "API Reference", level: 1 })).toBeVisible();
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

  test("is legible on both themes", async ({ page }) => {
    await authenticate(page);
    await page.goto(REFERENCE_PATH);
    await expect(page.locator(TAG_HEADING)).toBeVisible({ timeout: 30_000 });

    // ReDoc carries its own light theme. Handed it unchanged, a reader on the
    // dark theme gets near-black body copy on a near-black page.
    const light = await textLuminance(page, TAG_HEADING);

    await page.evaluate(() => localStorage.setItem("mcp-portal-theme", "dark"));
    await page.reload();
    await expect(page.locator(TAG_HEADING)).toBeVisible({ timeout: 30_000 });
    const dark = await textLuminance(page, TAG_HEADING);

    expect(light, "dark copy on the light page").toBeLessThan(0.4);
    expect(dark, "light copy on the dark page").toBeGreaterThan(0.6);

    // The sample tabs in the right-hand panel keep a white ground on both
    // themes, and ReDoc labels them with the same color it writes body copy
    // in: on the dark theme that is white on white, an empty pill where the
    // word "Payload" should be.
    expect(
      await inkGap(page, ".react-tabs__tab--selected"),
      "the selected sample tab is readable on its own ground",
    ).toBeGreaterThan(0.4);
  });
});

test.describe("The pointer from the operation browser", () => {
  test("opens the reference on the gateway routes", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/apis?connection=acme-billing&spec=core&op=createCustomer");

    const link = page.getByRole("link", {
      name: "Platform REST reference (auth, gateway routes, status codes)",
    });
    // The fragment is the one the served Swagger UI mints for a tag, so the
    // reader lands on the gateway routes rather than at the top of a document
    // that describes a few hundred paths.
    await expect(link).toHaveAttribute("href", "/api/v1/admin/docs/index.html#/Gateway");
  });
});
