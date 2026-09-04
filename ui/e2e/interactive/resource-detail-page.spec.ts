import { test, expect, type Page, type Locator } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// A managed resource opens at a route of its own (#1470). It used to open in a
// 32rem dialog over the library, which meant it could not be linked to,
// bookmarked, reloaded or opened in a second tab, and its content was drawn
// inside a box half the viewport tall inside a scrolling column.
//
// res-001 ("SQL Style Guide") is the fixture that makes the case: MSW answers
// its content route, so the content region renders, and it is the one resource
// carrying both a read-activity rollup and a three-revision trail.

const ADMIN_RESOURCES = "/portal/admin/resources";
const USER_RESOURCES = "/portal/resources";
const RESOURCE = "SQL Style Guide";

function panel(page: Page): Locator {
  return page.getByTestId("modal-panel");
}

// selectLibrary picks a library from the picker, which is one listbox now
// rather than a strip of tabs (#1553).
async function selectLibrary(page: Page, name: string): Promise<void> {
  await page.getByRole("combobox", { name: "Library" }).click();
  await page.getByRole("option", { name, exact: true }).click();
}

// openNamed reaches one file in a library by searching for it. A library is a
// tree (#1530), so a file is not on the page the library opens at -- it is
// inside whichever folder it is filed in, and searching reaches it from
// anywhere in the library.
async function openNamed(page: Page, name: string): Promise<void> {
  await page.getByLabel("Search resources").fill(name);
  await page.getByText(name, { exact: true }).first().click();
}

async function openDetail(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto(ADMIN_RESOURCES);
  await openNamed(page, RESOURCE);
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

// RESOURCE names a markdown document whose own first heading is its title, so
// a heading query has to be narrowed to the first match: the page header. An
// unscoped one matches the rendered document's <h1> as well and trips
// Playwright's strict mode.
test.describe("A resource has an address", () => {
  test("opening one from the library puts it in the address bar", async ({ page }) => {
    await openDetail(page);
    await expect(page).toHaveURL(/\/portal\/admin\/resources\/res-001$/);
    await expect(page.getByRole("heading", { name: RESOURCE }).first()).toBeVisible();
    // No dialog is involved any more: the resource is the page.
    await expect(panel(page)).toHaveCount(0);
  });

  test("a reload lands back on the same resource", async ({ page }) => {
    await openDetail(page);
    await page.reload();

    await expect(page.getByRole("heading", { name: RESOURCE }).first()).toBeVisible();
    await expect(page.getByTestId("resource-versions")).toBeVisible();
  });

  test("an id that names no resource says so", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/resources/res-nonesuch");

    await expect(page.getByTestId("resource-not-found")).toBeVisible();
    await expect(page.getByText("There is no page at this address.")).toHaveCount(0);
  });

  test("the page's own Back arrow returns to the library as it was left", async ({ page }) => {
    await authenticate(page);
    await page.goto(USER_RESOURCES);
    await selectLibrary(page, "Global");
    // The library is a route segment now, and the search a query parameter on
    // it (#1530).
    await expect(page).toHaveURL(/\/portal\/resources\/lib\/global$/);
    await page.getByLabel("Search resources").fill("SQL");
    await expect(page).toHaveURL(/q=SQL/);

    await page.getByText(RESOURCE, { exact: true }).first().click();
    await expect(page).toHaveURL(/\/portal\/resources\/res-001$/);

    // The arrow, not the browser button: it used to navigate to a bare
    // /resources, which dropped the scope and the filters (#1470).
    await page.getByRole("button", { name: "Back", exact: true }).click();
    await expect(page.getByRole("combobox", { name: "Library" })).toContainText("Global");
    await expect(page.getByLabel("Search resources")).toHaveValue("SQL");
  });

  test("the Back arrow on a cold deep link falls back to the library", async ({ page }) => {
    await authenticate(page);
    // No entry to return to: this document was loaded at the resource.
    await page.goto("/portal/resources/res-001");
    await page.getByRole("button", { name: "Back", exact: true }).click();

    await expect(page).toHaveURL(/\/portal\/resources$/);
    // The picker's own default, which is where a fallback lands.
    await expect(page.getByRole("combobox", { name: "Library" })).toContainText("All");
  });

  test("Back returns to the library with its scope and its search intact", async ({ page }) => {
    await authenticate(page);
    await page.goto(USER_RESOURCES);

    // The library opens on All; the case is about one library in particular.
    await selectLibrary(page, "Global");
    await expect(page).toHaveURL(/\/portal\/resources\/lib\/global$/);
    await page.getByLabel("Search resources").fill("SQL");
    await expect(page).toHaveURL(/q=SQL/);

    await page.getByText(RESOURCE, { exact: true }).first().click();
    await expect(page).toHaveURL(/\/portal\/resources\/res-001$/);

    await page.goBack();
    await expect(page.getByRole("combobox", { name: "Library" })).toContainText("Global");
    await expect(page.getByLabel("Search resources")).toHaveValue("SQL");
  });
});

test.describe("What the resource page offers", () => {
  test("keeps Download, Edit and Delete on screen beside the content", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 600 });
    await openDetail(page);

    // The dialog held these in a pinned footer because every section between
    // them and the top scrolled past. On the page they are in the header, so
    // scrolling to the end of the sidebar does not take them anywhere.
    await page.getByTestId("resource-versions").scrollIntoViewIfNeeded();
    await expect(page.getByTestId("resource-versions")).toBeInViewport();

    const actions = page.getByTestId("resource-detail-actions");
    for (const name of ["Download", "Edit", "Delete"]) {
      await expect(actions.getByRole("button", { name })).toBeVisible();
    }
  });

  test("carries the lifecycle surfaces the dialog carried", async ({ page }) => {
    await openDetail(page);

    await expect(page.getByTestId("resource-usage")).toBeVisible();
    await expect(page.getByTestId("resource-versions")).toBeVisible();
  });

  test("carries the table registration panel on a CSV", async ({ page }) => {
    await authenticate(page);
    await page.goto(ADMIN_RESOURCES);
    // The panel is absent unless the file is a CSV, which res-001 is not.
    await openNamed(page, "Business Glossary Export");

    await expect(page.getByText("Query as a table")).toBeVisible();
  });

  // A registration refusal is the one thing this panel exists to say, and it
  // was being clipped mid-word at the sidebar's edge on every line (#1617): the
  // correction it offers is a Button, which is nowrap and will not shrink, so
  // its label set a minimum width on the alert wider than the column and every
  // sentence beside it was laid out at that width. Nothing in jsdom measures a
  // box, so this is asserted in a real browser.
  test("keeps a registration refusal inside the column it is shown in", async ({ page }) => {
    await authenticate(page);
    await page.goto(ADMIN_RESOURCES);
    // The fixture whose cells carry line breaks, which is refused (#1441).
    await openNamed(page, "Store List");

    await page.getByTestId("tables-panel").scrollIntoViewIfNeeded();
    await page.getByRole("button", { name: "Register", exact: true }).first().click();
    await page.getByRole("button", { name: "Register", exact: true }).last().click();

    const alert = page.getByTestId("table-register-error");
    await expect(alert).toBeVisible();
    // Nothing inside the refusal is wider than the refusal, so no line of it
    // is cut off or pushed onto a scrollbar.
    expect(
      await alert.evaluate((el) => el.scrollWidth - el.clientWidth),
      "the refusal overflows its own box",
    ).toBeLessThanOrEqual(1);

    // And the panel it sits in is no wider than the sidebar column that clips
    // it, which is what the nowrap button was widening.
    const panelBox = (await page.getByTestId("tables-panel").boundingBox())!;
    const buttonBox = (await page.getByTestId("table-repair-button").boundingBox())!;
    expect(buttonBox.x + buttonBox.width).toBeLessThanOrEqual(panelBox.x + panelBox.width + 1);
  });
});

test.describe("Resource form modal geometry", () => {
  // Editing and uploading stay dialogs: they are bounded forms, which is the
  // shape ModalShell is for. Both take the capped shape with their submit in a
  // pinned footer. A cap without a working column would render that footer past
  // the panel's bottom edge, where it cannot be clicked.
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
