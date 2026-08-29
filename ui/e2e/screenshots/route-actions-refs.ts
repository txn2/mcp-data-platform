import { type Page } from "@playwright/test";
import { openAssetProvenance, openPersonaScopeTab } from "./route-actions";
import { openResourceNamed } from "./route-actions-library";

// The capture actions for the reference surface (#1475, #1488), both ends of
// it: the panel on the asset that lists what it depends on -- uploaded files
// and other assets alike -- and the section on the target that lists what
// depends on it.
//
// They are a module of their own because route-actions.ts is at its line
// ceiling, and a file that has to be split is better split along a surface
// than at whatever line the limit falls on.

/**
 * openAssetRefs opens the asset viewer's metadata sidebar and scrolls to what
 * the asset's content references (#1475, #1488). The sidebar is closed until
 * "Show details" is pressed, so the panel is not merely below the fold -- it is
 * not mounted.
 */
export async function openAssetRefs(page: Page): Promise<void> {
  await openAssetProvenance(page);
  await page
    .getByTestId("asset-refs")
    .scrollIntoViewIfNeeded({ timeout: 5_000 })
    .catch(() => {});
  await page.waitForTimeout(500);
}

/**
 * openAssetRefPicker opens the same panel's picker, which is the one state
 * naming what a reference gives away and who this asset is currently shared
 * with -- the sentence the reader is meant to read before confirming.
 */
export async function openAssetRefPicker(page: Page): Promise<void> {
  await openAssetRefs(page);
  await page.getByRole("button", { name: "Add" }).first().click({ timeout: 3_000 });
  await page.getByTestId("asset-ref-picker").waitFor({ timeout: 3_000 });
  await page.waitForTimeout(500);
}

/**
 * openAssetRefPickerAssets opens the picker on its second tab, the assets this
 * reader can open (#1488). It is a capture of its own because the tab is what
 * makes the second kind of reference reachable at all: a reader who never
 * pressed it would take the picker for a file picker.
 */
export async function openAssetRefPickerAssets(page: Page): Promise<void> {
  await openAssetRefPicker(page);
  await page.getByRole("tab", { name: "Assets" }).click({ timeout: 3_000 });
  await page.waitForTimeout(700);
}

/**
 * openAssetUsedBy scrolls the asset viewer's sidebar to the section naming the
 * assets that read this one's content (#1488) -- the reverse of the panel
 * above, and what tells an owner before an edit or a delete that something else
 * is serving from it.
 */
export async function openAssetUsedBy(page: Page): Promise<void> {
  await openAssetProvenance(page);
  await page
    .getByTestId("used-by-assets")
    .scrollIntoViewIfNeeded({ timeout: 5_000 })
    .catch(() => {});
  await page.waitForTimeout(500);
}

/**
 * openAssetThumbnail scrolls the sidebar to the panel showing the tile everyone
 * else sees of this asset, and the control that asks for it to be taken again
 * (#1497). It is a capture of its own because the tile and the way back from a
 * wrong one are what the panel exists to put in front of an owner.
 */
export async function openAssetThumbnail(page: Page): Promise<void> {
  await openAssetProvenance(page);
  await page
    .getByTestId("asset-thumbnail-panel")
    .scrollIntoViewIfNeeded({ timeout: 5_000 })
    .catch(() => {});
  await page.waitForTimeout(500);
}

/**
 * openResourceUsedByAssets opens a managed image an asset's content references
 * and scrolls to the section naming what is holding it up (#1475).
 *
 * It is a persona-scoped file rather than the global one the other resource
 * captures use, because the references in the fixture are to the images a
 * report actually embeds, and the flag this section exists for -- an asset with
 * a public link makes the file readable by anyone holding it -- only appears
 * where something references it.
 */
export async function openResourceUsedByAssets(page: Page): Promise<void> {
  await openPersonaScopeTab(page);
  await openResourceNamed(page, "Warehouse Floor Plan");
  await page
    .getByTestId("used-by-assets")
    .scrollIntoViewIfNeeded({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(500);
}
