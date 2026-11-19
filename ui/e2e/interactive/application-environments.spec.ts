import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Each environment its own schema (#2709), against MSW's "shop" (dev in
// shop_dev, prod in shop, site parts calling each and one calling nothing)
// and "warehouse" (one environment, still naming its schema wh).

test.describe("Application environments", () => {
  test("each environment shows its schema and sites, and each site part what it calls", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/applications/app-shop");
    await expect(page.getByTestId("environment-schema-dev")).toContainText("shop_dev");
    await expect(page.getByTestId("environment-schema-dev")).toContainText("on acme-shop · migration 3");
    await expect(page.getByTestId("environment-sites-prod")).toContainText("shop-web / prod");
    await expect(page.getByTestId("site-part-calls-shop-web/dev")).toContainText("shop / dev · release 3 · shop_dev");
    await expect(page.getByTestId("site-part-calls-shop-web/staging")).toContainText(
      "Nothing. shop has no environment named staging, so its /endpoints answer 404.",
    );
    await page.getByRole("button", { name: "Add environment staging" }).click();
    const dialog = page.getByRole("dialog", { name: "Add environment" });
    await expect(dialog.getByLabel("Name")).toHaveValue("staging");
    await expect(dialog.getByLabel("Schema")).toHaveAttribute("placeholder", "shop_staging");
    await dialog.getByRole("button", { name: "Cancel" }).click();
  });

  test("adds an environment in a schema of its own", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/applications/app-shop");
    await page.getByRole("button", { name: "Add environment", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Add environment" });
    await dialog.getByLabel("Name").fill("qa");
    await expect(dialog.getByText("On acme-shop, with its own run role. Built from prod's structure, with no rows.")).toBeVisible();
    await dialog.getByLabel("Schema").fill("shop_qa");
    await dialog.getByRole("button", { name: "Add" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByTestId("environment-schema-qa")).toContainText("shop_qa");
  });

  test("a promote lists what it does before doing it", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/applications/app-shop");
    await page.getByRole("button", { name: "Release", exact: true }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Release", exact: true }).click();
    await expect(page.getByLabel("dev runs release 4")).toBeVisible();
    await page.getByRole("button", { name: "Promote dev to prod" }).click();
    const dialog = page.getByTestId("promote-dialog");
    await expect(dialog.getByRole("heading", { name: "Promote release 4 to prod" })).toBeVisible();
    await expect(dialog.getByTestId("promote-list")).toContainText("applies migration 4 to shop");
    await expect(dialog.getByTestId("promote-list")).toContainText("moves shop-web / prod (coupled) to release 31");
    await expect(dialog).toContainText("Data is not copied. prod keeps its variables and its schema's rows; dev keeps its own.");
    await dialog.getByRole("button", { name: "Promote", exact: true }).click();
    await expect(page.getByLabel("prod runs release 4")).toBeVisible();
  });

  test("a site environment of no environment's name is held out of Add parts", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/applications/app-shop");
    await page.getByRole("button", { name: "Add parts" }).click();
    const dialog = page.getByRole("dialog", { name: "Add parts to shop" });
    await dialog.getByRole("tab", { name: /^Sites/ }).click();
    await expect(dialog.getByTestId("candidate-site-storefront/dev")).toContainText("calls shop / dev");
    await expect(dialog.getByRole("checkbox", { name: "shop-web / preview, no environment named preview" })).toBeDisabled();
    await expect(dialog.getByTestId("site-unmatched-shop-web/preview")).toContainText(
      "shop-web / preview can be added once shop has an environment named preview.",
    );
    await dialog.getByRole("button", { name: "Add environment preview" }).click();
    await expect(page.getByRole("dialog", { name: "Add environment" }).getByLabel("Name")).toHaveValue("preview");
  });

  test("an application still naming its schema prepares before adding an environment", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/applications/app-warehouse");
    const banner = page.getByTestId("prepare-banner");
    await expect(banner).toContainText("warehouse runs in prod only. 2 scripts, 1 library and 1 pending migration name the schema wh");
    await expect(page.getByRole("button", { name: "Add environment", exact: true })).toBeDisabled();
    await banner.getByRole("button", { name: "Review the change" }).click();
    const dialog = page.getByTestId("prepare-dialog");
    await expect(dialog.getByTestId("prepare-file-script-wh-01")).toContainText("Receive stock · version 17 → 18");
    await expect(dialog.getByTestId("prepare-file-migration-2")).toContainText("migration 2 (pending)");
    await expect(dialog.getByTestId("prepare-replay")).toContainText("Replayed 5 runs of the changed scripts: every answer is the same.");
    await dialog.getByRole("button", { name: "Save 4 new versions" }).click();
    await expect(dialog.getByText("Saved. Release the new versions to put them live, then add the environment.")).toBeVisible();
    await dialog.getByRole("button", { name: "Done" }).click();
    await expect(banner).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Add environment", exact: true })).toBeEnabled();
  });
});
