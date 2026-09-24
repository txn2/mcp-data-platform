import { type Page } from "@playwright/test";
import { fileManager, openResourceIn, searchFor, waitForFolder } from "./helpers/resources";

// Browsing the Resources file manager (#1872): reaching one file, walking into
// a folder, picking several files, the preview pane, the context menu, and a
// folder with nothing in it.
//
// They live apart from route-actions for the reason the asset-reference and
// persona helpers do -- one surface grown past what belongs in a shared file --
// and because the resource captures that act on a file's own page all start by
// reaching the file through one of these.

/**
 * openResourceNamed opens one managed resource on its own page from the
 * top-level folder it is filed in ("global", a persona, or "person:<id>" for
 * one person's own files), by searching that folder for it.
 *
 * A file is inside whichever folder it is filed in (#1530), and a search spans
 * the whole top-level folder, which keeps a capture from having to know where
 * a fixture happens to be filed.
 */
export async function openResourceNamed(page: Page, name: string, root = "global"): Promise<void> {
  await openResourceIn(page, root, name);
  await page.waitForTimeout(700);
}

/** openTopLevelFolder opens a top-level folder from the tree. */
export async function openTopLevelFolder(page: Page, root: string): Promise<void> {
  await page.getByTestId(`tree-node-${root}:`).click({ timeout: 3_000 });
  await waitForFolder(page, "");
  await page
    .locator(`[data-testid="tree-node-${root}:"][aria-current="location"]`)
    .waitFor({ state: "visible", timeout: 5_000 });
}

/**
 * openResourceFolder walks into one folder of the folder in view.
 *
 * The folder's row is the control, and a folder opens on a single click. The
 * wait is on the path bar reaching that folder rather than on a pause, because
 * a swallowed click would publish the parent captioned as the folder.
 */
export async function openResourceFolder(page: Page, name: string): Promise<void> {
  const row = page
    .getByTestId("listing")
    .locator('tr[data-key^="d:"]')
    .filter({ has: page.getByText(name, { exact: true }) })
    .first();
  const key = await row.getAttribute("data-key", { timeout: 3_000 });
  await row.click({ timeout: 3_000 });
  await waitForFolder(page, (key ?? "").slice(2));
  await page.waitForTimeout(400);
}

/**
 * openResourceSubfolder drills two levels into a persona's folder: the tree
 * shows the folder open with its ancestors expanded and highlighted, the path
 * bar names each level, and the listing is that folder's own files.
 */
export async function openResourceSubfolder(page: Page): Promise<void> {
  await openTopLevelFolder(page, "inventory-analyst");
  await openResourceFolder(page, "reference");
  await openResourceFolder(page, "dictionaries");
}

/** selectFiles clicks the first file of the folder in view and Shift-clicks the nth. */
async function selectFiles(page: Page, count: number): Promise<void> {
  const files = page.getByTestId("listing").locator('tr[data-key]:not([data-key^="d:"])');
  await files.first().click({ timeout: 3_000 });
  await files.nth(count - 1).click({ modifiers: ["Shift"], timeout: 3_000 });
  await page.getByTestId("selection-bar").getByText(`${count} selected`).waitFor({ state: "visible", timeout: 5_000 });
}

/**
 * openResourceSelection picks two files in a folder and opens the move dialog
 * over them (#1530). Re-filing forty resources used to mean opening forty Edit
 * dialogs.
 */
export async function openResourceSelection(page: Page): Promise<void> {
  // A folder holding several files, so the capture shows a selection of more
  // than one against the rows it was made from.
  await openTopLevelFolder(page, "data-engineer");
  await openResourceFolder(page, "runbooks");
  await selectFiles(page, 2);
  await page.getByTestId("selection-bar").getByRole("button", { name: "Move to..." }).click({ timeout: 3_000 });
  await page.getByTestId("move-picker").waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(400);
}

/**
 * openResourceMultiSelect picks several files with Shift-click (#1872): the
 * selection bar with its bulk actions, the rows marked, and the preview pane
 * giving the count and the total size.
 */
export async function openResourceMultiSelect(page: Page): Promise<void> {
  await openTopLevelFolder(page, "data-engineer");
  await openResourceFolder(page, "visual");
  await selectFiles(page, 3);
  await page.getByTestId("preview-many").waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(400);
}

/**
 * openResourcePreview selects one file (#1872): a single click previews it in
 * the pane beside the listing, with its tile, where it is, its URI and its
 * tags, without leaving the folder.
 */
export async function openResourcePreview(page: Page): Promise<void> {
  await openTopLevelFolder(page, "global");
  await openResourceFolder(page, "documentation");
  await page.getByTestId("row-res-001").click({ timeout: 3_000 });
  await page.getByTestId("preview-file").waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(800);
}

/**
 * openResourceContextMenu right-clicks a file (#1872): Open, Rename, Move to,
 * Tag, Copy URI and Delete, at the pointer.
 */
export async function openResourceContextMenu(page: Page): Promise<void> {
  await openTopLevelFolder(page, "global");
  await openResourceFolder(page, "documentation");
  await page.getByTestId("row-res-001").click({ button: "right", timeout: 3_000 });
  await page.getByTestId("context-menu").waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(300);
}

/**
 * openResourceEmptyFolder opens a folder stored with nothing in it (#1872):
 * Global's `templates/drafts` in the mock. It says where a drop or an upload
 * would be filed.
 */
export async function openResourceEmptyFolder(page: Page): Promise<void> {
  await openTopLevelFolder(page, "global");
  await openResourceFolder(page, "templates");
  await openResourceFolder(page, "drafts");
  await fileManager(page).getByTestId("resources-empty").waitFor({ state: "visible", timeout: 5_000 });
}

/**
 * openResourceSearch runs a search that finds things in more than one folder.
 *
 * The sentence this illustrates is that a search spans the whole top-level
 * folder, whichever folder it was typed in, and that each hit names where it
 * is. A search matching nothing demonstrates neither.
 */
export async function openResourceSearch(page: Page): Promise<void> {
  await openTopLevelFolder(page, "global");
  await openResourceFolder(page, "documentation");
  await searchFor(page, "data");
  await page.waitForTimeout(600);
}
