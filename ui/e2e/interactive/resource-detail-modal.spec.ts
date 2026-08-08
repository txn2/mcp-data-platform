import { test, expect, type Page, type Locator } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The resource modals are the tallest the portal opens: the detail read stacks
// a preview frame plus usage, version-history and used-by panels into one panel
// whose height is bounded by nothing in the resource itself (#1233). Each has to
// cap at the viewport and scroll its body, rather than grow past the bottom edge
// and take its own title bar off the top of the screen with it.
//
// Geometry is keyed on the shell's `modal-panel` testid rather than the dialog's
// accessible name, so a case can only fail for a height or an offset -- not for
// a renamed label.
//
// res-001 ("SQL Style Guide") is the fixture that makes the case: MSW answers
// its content route, so the preview frame renders, and it is the one resource
// carrying both a read-activity rollup and a three-revision trail.

const ADMIN_RESOURCES = "/portal/admin/resources";
const RESOURCE = "SQL Style Guide";

function panel(page: Page): Locator {
  return page.getByTestId("modal-panel");
}

async function openDetail(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto(ADMIN_RESOURCES);
  await page.getByText(RESOURCE, { exact: true }).first().click();
  await expect(page.getByTestId("resource-versions")).toBeVisible();
}

// expectWithinViewport asserts the element is wholly on screen, which is what
// "reachable" means for a control the modal exists to offer.
async function expectWithinViewport(target: Locator, height: number): Promise<void> {
  const box = await target.boundingBox();
  expect(box).not.toBeNull();
  expect(box!.y).toBeGreaterThanOrEqual(0);
  expect(box!.y + box!.height).toBeLessThanOrEqual(height + 1);
}

test.describe("Resource detail dialog geometry", () => {
  for (const height of [900, 500]) {
    test(`fits inside a ${height}px viewport`, async ({ page }) => {
      await page.setViewportSize({ width: 1440, height });
      await openDetail(page);

      const box = await panel(page).boundingBox();
      expect(box).not.toBeNull();
      // Both edges: a panel taller than the viewport spills off the bottom, and
      // a centred one that overflows puts its top above the scroll container's
      // start, where nothing can reach it.
      expect(box!.y).toBeGreaterThanOrEqual(0);
      expect(box!.height).toBeLessThanOrEqual(height);
      expect(box!.y + box!.height).toBeLessThanOrEqual(height + 1);
    });
  }

  test("holds the title, close button and actions while the body scrolls", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 600 });
    await openDetail(page);

    // Scroll to the last panel in the stack. On the natural-height shape this
    // moves the whole panel, title bar included, off the top of the screen.
    await page.getByTestId("resource-versions").scrollIntoViewIfNeeded();
    await expect(page.getByTestId("resource-versions")).toBeInViewport();

    await expectWithinViewport(page.getByRole("heading", { name: RESOURCE }), 600);
    await expectWithinViewport(panel(page).getByRole("button", { name: "Close" }), 600);

    // The preview frame and every version row offer a Download of their own, so
    // the pinned row is addressed as the region it is.
    const actions = page.getByTestId("resource-detail-actions");
    for (const name of ["Download", "Edit", "Delete"]) {
      await expectWithinViewport(actions.getByRole("button", { name }), 600);
    }
  });

  test("is dismissable by Escape and by the close button on a short viewport", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 500 });
    await openDetail(page);

    await page.keyboard.press("Escape");
    await expect(panel(page)).toHaveCount(0);

    await page.getByText(RESOURCE, { exact: true }).first().click();
    await expect(panel(page)).toBeVisible();
    await panel(page).getByRole("button", { name: "Close" }).click();
    await expect(panel(page)).toHaveCount(0);
  });
});

test.describe("Resource form modal geometry", () => {
  // The edit and upload forms were moved to the same capped shape, with their
  // submit in the pinned footer. A cap without a working column would render
  // that footer past the panel's bottom edge, where it cannot be clicked.
  test("keeps Save reachable in the edit form on a short viewport", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 420 });
    await openDetail(page);

    await page.getByTestId("resource-detail-actions").getByRole("button", { name: "Edit" }).click();
    await expect(page.getByRole("dialog", { name: "Edit Resource" })).toBeVisible();

    await expectWithinViewport(panel(page), 420);
    await expectWithinViewport(panel(page).getByRole("button", { name: "Save" }), 420);
  });

  test("keeps Upload reachable in the upload form on a short viewport", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 420 });
    await authenticate(page);
    await page.goto(ADMIN_RESOURCES);

    await page.getByRole("button", { name: "Upload", exact: true }).first().click();
    await expect(page.getByRole("dialog", { name: "Upload Resource" })).toBeVisible();

    await expectWithinViewport(panel(page), 420);
    await expectWithinViewport(panel(page).getByRole("button", { name: "Upload" }), 420);
  });
});
