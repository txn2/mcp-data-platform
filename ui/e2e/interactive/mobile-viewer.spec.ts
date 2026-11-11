import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The portal read at phone width (#1693). A phone is where a share link is
// opened, and the viewer was wider than the screen there: the page scrolled
// sideways and the action row was pushed off the left edge, so Feedback and the
// Shared badge could only be read by panning.
//
// Two rules produced it, and neither is visible in a component's props -- they
// are what the browser does with `flex shrink-0` and `w-80` once the viewport
// is narrower than the row and the column they size. So both are asserted
// against a rendered page rather than a unit test.
//
// ast-ext-001 is the fixture that makes the case: an asset owned by someone
// else and shared read-only, so the action row carries Feedback, the Shared
// badge, Download, Save to My Assets and the details toggle at once. That is
// the widest row the viewer draws.

const PHONE = { width: 400, height: 800 };
const DESKTOP = { width: 1440, height: 900 };

// Signing in happens at desktop width and the viewport is narrowed afterwards.
// The portal's nav rail is not rendered at phone width -- the menu button opens
// it as an overlay instead -- and the shared sign-in helper waits on the rail.
async function openAtPhoneWidth(page: Page, path: string): Promise<void> {
  await page.setViewportSize(DESKTOP);
  await authenticate(page);
  await page.setViewportSize(PHONE);
  await page.goto(path);
}

// expectNoSideScroll is the whole acceptance: a phone reader never pans. One
// pixel of tolerance absorbs sub-pixel layout rounding, which a correct page
// still produces at a fractional device pixel ratio.
async function expectNoSideScroll(page: Page): Promise<void> {
  const measured = await page.evaluate(() => {
    const main = document.querySelector("main");
    const root = document.documentElement;
    return {
      main: main ? main.scrollWidth - main.clientWidth : 0,
      document: root.scrollWidth - root.clientWidth,
    };
  });
  expect(measured.main, "the page area scrolls sideways at 400px").toBeLessThanOrEqual(1);
  expect(measured.document, "the document scrolls sideways at 400px").toBeLessThanOrEqual(1);
}

// expectActionsOnScreen asserts every header action is wholly within the
// screen, on both edges. The right edge alone is not enough: the row is
// right-aligned, so a row too wide to fit hangs off the LEFT, and the actions
// that fall off are the ones written first.
async function expectActionsOnScreen(page: Page): Promise<void> {
  const actions = page.getByTestId("page-header-actions").first();
  await expect(actions).toBeVisible();
  // Every child, not only the buttons: the slot also holds badges (a script's
  // state, a shared asset's permission), and one of those off the edge is the
  // same defect.
  const boxes = await actions.evaluate((row) =>
    Array.from(row.children).map((el) => {
      const r = el.getBoundingClientRect();
      return { left: r.left, right: r.right, text: (el.textContent ?? "").trim() || "(icon)" };
    }),
  );
  expect(boxes.length, "no header actions were found to check").toBeGreaterThan(0);
  for (const b of boxes) {
    expect(b.left, `"${b.text}" starts off the left edge of a ${PHONE.width}px screen`).toBeGreaterThanOrEqual(-1);
    expect(b.right, `"${b.text}" ends past the right edge of a ${PHONE.width}px screen`).toBeLessThanOrEqual(
      PHONE.width + 1,
    );
  }
}

// expectDetailsStacked asserts the details column sits below the content rather
// than beside it. A fixed 320px column beside the content does not by itself
// make the page scroll -- the content column carries min-w-0 and collapses
// instead, to about a tenth of the screen -- so this is the assertion that
// holds the sidebar rule, and no-side-scroll is the one that holds the row.
async function expectDetailsStacked(page: Page): Promise<void> {
  const content = await page.getByTestId("viewer-content").boundingBox();
  const details = await page.getByTestId("viewer-details").boundingBox();
  expect(content, "the content column is not rendered").not.toBeNull();
  expect(details, "the details column is not rendered").not.toBeNull();
  expect(
    details!.y,
    "the details column is drawn beside the content instead of under it",
  ).toBeGreaterThanOrEqual(content!.y + content!.height - 1);
  expect(
    content!.width,
    "the content column is squeezed by a column beside it",
  ).toBeGreaterThan(PHONE.width * 0.8);
}

// Leaves the details column showing. A resource opens with it already out
// (ResourceViewerPage passes sidebarInitiallyOpen), an asset opens without it.
async function showDetails(page: Page): Promise<void> {
  const open = page.getByTitle("Hide details");
  if ((await open.count()) === 0) {
    const toggle = page.getByTitle("Show details");
    await expect(toggle).toBeVisible();
    await toggle.click();
  }
  await expect(open).toBeVisible();
}

// An asset's details open from More on a phone (#2601); its toolbar is the
// one row under the title.
async function showAssetDetails(page: Page): Promise<void> {
  await page.getByTestId("phone-toolbar").getByRole("button", { name: "More" }).click();
  await page.getByRole("menuitem", { name: "Details" }).click();
  await expect(page.getByTestId("viewer-details")).toBeVisible();
}

// expectToolbarOnScreen asserts a phone's toolbar is one row wholly on the
// screen: every control on one line, none past either edge (#2601).
async function expectToolbarOnScreen(page: Page): Promise<void> {
  const row = page.getByTestId("phone-toolbar");
  await expect(row).toBeVisible();
  const boxes = await row.evaluate((el) =>
    Array.from(el.querySelectorAll("button")).map((b) => {
      const r = b.getBoundingClientRect();
      return { left: r.left, right: r.right, top: Math.round(r.top + r.height / 2), name: b.getAttribute("aria-label") ?? b.textContent ?? "" };
    }),
  );
  expect(new Set(boxes.map((b) => b.top)).size, "the toolbar wraps onto more than one row").toBe(1);
  for (const b of boxes) {
    expect(b.left, `"${b.name}" starts off the left edge`).toBeGreaterThanOrEqual(-1);
    expect(b.right, `"${b.name}" ends past the right edge`).toBeLessThanOrEqual(PHONE.width + 1);
  }
}

test.describe("The viewer fits a phone screen", () => {
  test("a shared asset stacks its details under the content and draws no side scroll", async ({ page }) => {
    await openAtPhoneWidth(page, "/portal/assets/ast-ext-001");
    // The share state is the quiet line under the title (#2601).
    await expect(page.getByTestId("phone-subtitle")).toContainText("Shared with you · Viewer");
    await showAssetDetails(page);
    await expectDetailsStacked(page);
    await expectNoSideScroll(page);
    await expectToolbarOnScreen(page);
  });

  test("a shared asset's Feedback, Download and Save to My Assets are all reachable", async ({ page }) => {
    await openAtPhoneWidth(page, "/portal/assets/ast-ext-001");
    await showAssetDetails(page);
    // Feedback and More are on the toolbar row; Download and Save to My Assets
    // are in More (#2601). Each is wholly on screen where it is drawn.
    const expectOnScreen = async (name: string, box: { x: number; width: number } | null) => {
      expect(box, `${name} is not rendered`).not.toBeNull();
      expect(box!.x, `${name} starts off the left edge`).toBeGreaterThanOrEqual(-1);
      expect(box!.x + box!.width, `${name} ends past the right edge`).toBeLessThanOrEqual(PHONE.width + 1);
    };
    const row = page.getByTestId("phone-toolbar");
    for (const name of [/^Feedback/, /^More$/]) {
      await expectOnScreen(String(name), await row.getByRole("button", { name }).boundingBox());
    }
    await row.getByRole("button", { name: "More" }).click();
    for (const name of ["Download", "Save to My Assets"]) {
      await expectOnScreen(name, await page.getByRole("menuitem", { name }).boundingBox());
    }
  });

  test("a managed resource stacks its details under the content and draws no side scroll", async ({ page }) => {
    await openAtPhoneWidth(page, "/portal/admin/resources/res-001");
    await expect(page.getByTestId("resource-versions")).toBeVisible();
    await showDetails(page);
    await expectDetailsStacked(page);
    await expectNoSideScroll(page);
    await expectActionsOnScreen(page);
  });

  test("a prompt draws no side scroll", async ({ page }) => {
    await openAtPhoneWidth(page, "/portal/prompts/prompt-010");
    await expectNoSideScroll(page);
    await expectActionsOnScreen(page);
  });

  test("a managed script draws no side scroll", async ({ page }) => {
    await openAtPhoneWidth(page, "/portal/automations/script-001");
    await expectNoSideScroll(page);
    await expectActionsOnScreen(page);
  });
});

// On a phone an asset opens with one title block and one toolbar row, and its
// content opens edge to edge (#2390, #2601). ast-deck is an HTML document, the
// type whose own controls (Present, Overview, Export PDF) wrapped onto a row of
// their own; the asset is the reader's own, so Share is a button and Download,
// Add to collection and Delete are in More.
test.describe("An asset on a phone (#2390, #2601)", () => {
  test("is a title block and one toolbar row, with the rest in More", async ({ page }) => {
    await openAtPhoneWidth(page, "/portal/assets/ast-deck");
    await expect(page.getByTestId("phone-title")).toBeVisible();
    // The section's name in the app header, and no platform version.
    await expect(page.locator("header h1")).toHaveText("Assets");
    await expect(page.locator("header").getByText(/^v\d+\./)).toHaveCount(0);
    await expectToolbarOnScreen(page);
    const row = page.getByTestId("phone-toolbar");
    await expect(row.getByRole("button", { name: /Download/ })).toHaveCount(0);
    // The version picker is in More > Versions, not on the row.
    await expect(page.getByRole("combobox", { name: "Asset version" })).toHaveCount(0);
    // The content starts under the title block and the one row.
    const frame = await page.locator('iframe[title="HTML Preview"]').boundingBox();
    expect(frame, "the document is not drawn").not.toBeNull();
    expect(frame!.y, "the content starts too far down the screen").toBeLessThanOrEqual(210);
    await row.getByRole("button", { name: "More" }).click();
    for (const name of ["Present", "Export PDF", "Download", "Delete"]) {
      await expect(page.getByRole("menuitem", { name })).toBeVisible();
    }
    await expect(page.getByRole("menuitem", { name: /Versions/ })).toBeVisible();
  });

  test("Expand fills the screen with the document, and Back closes it", async ({ page }) => {
    await openAtPhoneWidth(page, "/portal/assets/ast-deck");
    const frame = page.locator("iframe").first();
    await expect(frame).toBeVisible();
    await page.getByRole("button", { name: /Expand/ }).click();

    const view = page.getByRole("dialog", { name: /./ }).filter({ has: page.locator("iframe") });
    await expect(view).toBeVisible();
    await expect(page).toHaveURL(/[?&]view=full/);
    const box = await frame.boundingBox();
    expect(box, "the frame is not drawn").not.toBeNull();
    expect(box!.width, "the frame does not take the screen's width").toBeGreaterThan(PHONE.width - 24);
    expect(box!.height, "the frame does not take the screen's height").toBeGreaterThan(PHONE.height - 80);
    await expectNoSideScroll(page);

    await page.goBack();
    await expect(view).toHaveCount(0);
    await expect(page).not.toHaveURL(/view=full/);
    await expect(page.getByTestId("phone-toolbar")).toBeVisible();
  });

  test("a link with ?view=full opens expanded, and Close returns to the page", async ({ page }) => {
    await openAtPhoneWidth(page, "/portal/assets/ast-deck?view=full");
    await expect(page.getByRole("button", { name: "Close" })).toBeVisible();
    await page.getByRole("button", { name: "Close" }).click();
    await expect(page.getByRole("button", { name: "Close" })).toHaveCount(0);
    await expect(page).not.toHaveURL(/view=full/);
    await expect(page).toHaveURL(/\/portal\/assets\/ast-deck$/);
  });
});
