import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The asset page of a slide deck (#1769). Two rules of the page are held here,
// and neither is visible in a component's props: the deck's controls share the
// row the Preview/Source toggle and the version picker are on rather than
// taking a row of their own, and the frame takes the height left under that
// row, ending inside the viewport at the page's own padding. Both are what the
// browser does with a flex column, so both are asserted against a rendered
// page.
//
// ast-deck is the mock library's deck: an HTML asset that loads the served
// runtime, owned by the signed-in reader so the page carries the toggle and
// the picker.

const SIZES = [
  { width: 1440, height: 900 },
  { width: 1920, height: 1080 },
];

async function openDeck(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/assets/ast-deck");
  await expect(page.getByTestId("viewer-content").locator("iframe")).toBeVisible();
}

/** The vertical band a locator's box occupies. */
async function band(page: Page, selector: string): Promise<{ top: number; bottom: number }> {
  const box = await page.locator(selector).first().boundingBox();
  expect(box, `${selector} has no box`).not.toBeNull();
  return { top: box!.y, bottom: box!.y + box!.height };
}

for (const size of SIZES) {
  test.describe(`at ${size.width}x${size.height}`, () => {
    test.use({ viewport: size });

    test("the deck's controls share the row with the view toggle and the version picker", async ({ page }) => {
      await openDeck(page);
      const row = page.getByTestId("content-controls");
      await expect(row.getByRole("button", { name: "Present" })).toBeVisible();
      await expect(row.getByRole("button", { name: "Overview" })).toBeVisible();
      await expect(row.getByRole("button", { name: "Export PDF" })).toBeVisible();

      // Same row: the toggle and the controls overlap vertically, and the
      // controls sit to the right of the picker.
      const toggle = await band(page, '[role="group"][aria-label="Content view"]');
      const present = await row.getByRole("button", { name: "Present" }).boundingBox();
      const picker = await page.getByRole("combobox").first().boundingBox();
      expect(present).not.toBeNull();
      expect(picker).not.toBeNull();
      expect(present!.y).toBeLessThan(toggle.bottom);
      expect(present!.y + present!.height).toBeGreaterThan(toggle.top);
      expect(present!.x).toBeGreaterThan(picker!.x + picker!.width);
    });

    test("the frame ends inside the viewport, at the page's padding, with nothing to scroll", async ({ page }) => {
      await openDeck(page);
      const frame = await band(page, '[data-testid="viewer-content"] iframe');
      const main = await page.locator("main").boundingBox();
      expect(main).not.toBeNull();
      const padding = await page.locator("main").evaluate((m) => parseFloat(getComputedStyle(m).paddingBottom));

      // The frame's bottom edge is the page area's bottom edge less its padding.
      expect(frame.bottom).toBeLessThanOrEqual(size.height);
      expect(Math.abs(main!.y + main!.height - padding - frame.bottom)).toBeLessThanOrEqual(1);
      // And the frame is the tallest thing on the page: most of the area under
      // the header is the deck.
      expect(frame.bottom - frame.top).toBeGreaterThan(size.height * 0.5);

      const scroll = await page.locator("main").evaluate((m) => m.scrollHeight - m.clientHeight);
      expect(scroll, "the page area scrolls").toBeLessThanOrEqual(1);
    });
  });
}
