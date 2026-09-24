import { expect, type Locator, type Page } from "@playwright/test";

// Driving the Resources file manager (#1872), shared by the interactive specs
// and the screenshot captures. The page is a folder tree, one listing of the
// folder in view, and a preview pane; where it stands is in the address:
// `<section>/lib/<top-level folder>/<folder path>`, the top-level folder being
// "user", "global", a persona name, or "person:<id>" for an administrator.

export const ADMIN_RESOURCES = "/portal/admin/resources";
export const USER_RESOURCES = "/portal/resources";

/** The address of one folder of the file manager. */
export function folderURL(section: string, root: string, path = ""): string {
  const at = `${section}/lib/${encodeURIComponent(root)}`;
  return path ? `${at}/${path}` : at;
}

/** The file manager itself, which every locator below is scoped to. */
export function fileManager(page: Page): Locator {
  return page.getByTestId("file-manager");
}

/** One row of the listing, by the name it shows. */
export function rowNamed(page: Page, name: string): Locator {
  return page
    .getByTestId("listing")
    .locator("tr[data-key]")
    .filter({ has: page.getByText(name, { exact: true }) });
}

/**
 * gotoFolder loads one folder and waits until the listing stands in it: the
 * path bar's last segment is that folder, so a capture or an assertion that
 * follows is about the folder asked for rather than the page as it opened.
 */
export async function gotoFolder(page: Page, section: string, root: string, path = ""): Promise<void> {
  await page.goto(folderURL(section, root, path));
  await waitForFolder(page, path);
}

/** Waits for the path bar to name a folder and the listing to finish loading. */
export async function waitForFolder(page: Page, path: string): Promise<void> {
  await expect(page.getByTestId(`crumb-${path || "root"}`)).toBeVisible({ timeout: 10_000 });
  await expect(fileManager(page).getByText("Loading...")).toHaveCount(0, { timeout: 10_000 });
}

/** The file manager's own search box. */
export function searchBox(page: Page): Locator {
  return fileManager(page).getByLabel("Search", { exact: true });
}

/**
 * searchFor types a search into the file manager and waits for the hits. A
 * search spans the whole top-level folder in view, whichever folder it was
 * typed in, and each hit names where it is.
 */
export async function searchFor(page: Page, query: string): Promise<void> {
  await searchBox(page).fill(query);
  await expect(page.getByTestId("search-note")).toBeVisible({ timeout: 10_000 });
  await expect(fileManager(page).getByText("Loading...")).toHaveCount(0, { timeout: 10_000 });
}

/**
 * openNamed opens one file of the top-level folder in view on its own page, by
 * searching for it, selecting the hit, and pressing Open in the preview pane.
 *
 * A single click selects a file and previews it. The pane's Open rather than a
 * double-click, because the selection bar that the first click puts above the
 * listing moves every row down before the second click lands (see
 * resource-detail-page.spec.ts, "a double-click on a file opens it").
 */
export async function openNamed(page: Page, name: string): Promise<void> {
  await searchFor(page, name);
  const hit = rowNamed(page, name).first();
  await expect(hit).toBeVisible({ timeout: 10_000 });
  await hit.click();
  const pane = page.getByTestId("preview-file");
  await expect(pane.getByRole("heading", { name, exact: true })).toBeVisible();
  await pane.getByRole("button", { name: "Open", exact: true }).click();
  await expect(page).toHaveURL(/\/resources\/res-[^/?]+$/);
}

/**
 * openResourceIn opens one file from a named top-level folder, from a cold
 * load of that folder. The section is the one the page is in now, so an
 * administrator's capture stays on the administrator's page.
 */
export async function openResourceIn(page: Page, root: string, name: string): Promise<void> {
  const section = new URL(page.url()).pathname.startsWith(ADMIN_RESOURCES) ? ADMIN_RESOURCES : USER_RESOURCES;
  await gotoFolder(page, section, root);
  await openNamed(page, name);
}

/** Opens the Upload menu in the path bar and picks one of its two items. */
export async function chooseUpload(page: Page, item: "A file..." | "Many files or a folder..."): Promise<void> {
  await fileManager(page).getByRole("button", { name: "Upload", exact: true }).first().click();
  await page.getByRole("menuitem", { name: item }).click();
}
