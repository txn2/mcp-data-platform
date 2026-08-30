import { type Page } from "@playwright/test";

// Browsing a managed-resource library: reaching one file, walking into a
// folder, picking several files, and the view with nothing in it.
//
// They live apart from route-actions for the reason the asset-reference and
// persona helpers do -- one surface grown past what belongs in a shared file --
// and because the resource captures that act on a file's own page all start by
// reaching the file through one of these.

/**
 * openResourceNamed opens one managed resource from a library, by searching for
 * it.
 *
 * A library is a tree now (#1530), so a file is not on the page the library
 * opens at -- it is inside whichever folder it is filed in. Searching reaches it
 * from anywhere in the library, which is what the search is for, and it keeps a
 * capture from having to know where a fixture happens to be filed.
 */
export async function openResourceNamed(page: Page, name: string): Promise<void> {
  await page.getByLabel("Search resources").fill(name);
  // The search is debounced against the query, so the hit list is what is
  // waited on rather than a fixed pause.
  await page.getByText(name, { exact: true }).first().waitFor({ state: "visible", timeout: 5_000 });
  await page.getByText(name, { exact: true }).first().click({ timeout: 3_000 });
  await page.waitForTimeout(700);
}

/**
 * openResourceFolder walks into one folder of the library in view.
 *
 * The folder row is the control, the way every other portal list opens on row
 * click; the wait is on the breadcrumb reaching that folder rather than on a
 * pause, because a swallowed click would publish the library root captioned as
 * a folder.
 */
export async function openResourceFolder(page: Page, name: string): Promise<void> {
  await page.getByTestId(new RegExp(`^folder-row-.*${name}$`)).first().click({ timeout: 3_000 });
  await page
    .getByLabel("Folder path")
    .first()
    .getByText(name, { exact: true })
    .waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(400);
}

/**
 * openResourceSubfolder drills two levels in, which is what a tree is for: the
 * root shows the top folder, and the level below it is only reachable by
 * opening one.
 */
export async function openResourceSubfolder(page: Page): Promise<void> {
  await openResourceFolder(page, "reference");
  await openResourceFolder(page, "dictionaries");
}

/**
 * openResourceSelection picks two files in a folder and opens the move dialog
 * over them (#1530). Re-filing forty resources used to mean opening forty Edit
 * dialogs.
 */
export async function openResourceSelection(page: Page): Promise<void> {
  // A folder holding several files, so the capture shows a selection of more
  // than one against the rows it was made from.
  await openResourceFolder(page, "runbooks");
  // The first checkbox is the header's select-all; the rows follow it.
  const boxes = page.getByRole("checkbox");
  await boxes.nth(1).check({ timeout: 3_000 });
  await boxes.nth(2).check({ timeout: 3_000 });
  await page.getByTestId("selection-bar").waitFor({ state: "visible", timeout: 5_000 });
  await page.getByRole("button", { name: "Move", exact: true }).click({ timeout: 3_000 });
  await page.getByLabel("Destination folder").waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(400);
}

/**
 * openResourceSearch runs a library-wide search that finds things.
 *
 * The sentence this illustrates is that search spans the whole library and
 * that each hit names the path it was found at. A search matching nothing
 * demonstrates neither, so the query is one that returns files from more than
 * one folder.
 */
export async function openResourceSearch(page: Page): Promise<void> {
  // A term that matches files filed under DIFFERENT folders, because the point
  // of the capture is that search spans the whole library and each hit names
  // the path it was found at. A query that matched nothing showed neither.
  await page.getByLabel("Search resources").fill("sql");
  await page
    .getByTestId("resources-empty")
    .waitFor({ state: "detached", timeout: 5_000 })
    .catch(() => {});
  await page.waitForTimeout(600);
}
