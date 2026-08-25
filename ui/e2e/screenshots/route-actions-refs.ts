import { type Page } from "@playwright/test";
import { openAssetProvenance, openPersonaScopeTab } from "./route-actions";

// The capture actions for the asset-to-resource reference surface (#1475),
// both ends of it: the panel on the asset that lists what it depends on, and
// the section on the resource that lists what depends on it.
//
// They are a module of their own because route-actions.ts is at its line
// ceiling, and a file that has to be split is better split along a surface
// than at whatever line the limit falls on.

/**
 * openAssetResourceRefs opens the asset viewer's metadata sidebar and scrolls to
 * the managed resources the asset's content references (#1475). The sidebar is
 * closed until "Show details" is pressed, so the panel is not merely below the
 * fold -- it is not mounted.
 */
export async function openAssetResourceRefs(page: Page): Promise<void> {
  await openAssetProvenance(page);
  await page
    .getByTestId("asset-resource-refs")
    .scrollIntoViewIfNeeded({ timeout: 5_000 })
    .catch(() => {});
  await page.waitForTimeout(500);
}

/**
 * openAssetResourceRefPicker opens the same panel's picker, which is the one
 * state naming what a reference gives away and who this asset is currently
 * shared with -- the sentence the reader is meant to read before confirming.
 */
export async function openAssetResourceRefPicker(page: Page): Promise<void> {
  await openAssetResourceRefs(page);
  await page.getByRole("button", { name: "Add" }).first().click({ timeout: 3_000 });
  await page.getByTestId("asset-resource-picker").waitFor({ timeout: 3_000 });
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
  await page
    .getByText("Warehouse Floor Plan", { exact: true })
    .first()
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(700);
  await page
    .getByTestId("resource-used-by-assets")
    .scrollIntoViewIfNeeded({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(500);
}
