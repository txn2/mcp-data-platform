import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";
import { USER_RESOURCES, fileManager, gotoFolder, rowNamed, searchFor } from "../screenshots/helpers/resources";

// The file manager's search covers every folder the caller can open (#2293):
// a person who cannot place a file searches for it, so a search started in
// My Resources finds a Global file and says where it is. Keeping the search
// in the open folder is a choice beside the box, off until it is made.

test.beforeEach(async ({ page }) => {
  await authenticate(page);
});

test("a search from My Resources finds a Global file and names its folder", async ({ page }) => {
  await gotoFolder(page, USER_RESOURCES, "user");
  await searchFor(page, "onboarding");
  await expect(page.getByTestId("search-note")).toContainText("Searching every folder you can open");
  const hit = rowNamed(page, "Onboarding Guide").first();
  await expect(hit).toContainText("/Global/onboarding");
  await expect(fileManager(page).getByTestId("search-within")).not.toBeChecked();

  await hit.getByRole("button", { name: "Show in folder" }).click();
  await expect(page).toHaveURL(/\/portal\/resources\/lib\/global\/onboarding$/);
  await expect(page.getByTestId("crumb-onboarding")).toBeVisible();
});

test("This folder only keeps the search in the open folder and beneath it", async ({ page }) => {
  await gotoFolder(page, USER_RESOURCES, "user");
  await searchFor(page, "onboarding");
  await fileManager(page).getByLabel("This folder only").check();
  await expect(page).toHaveURL(/within=1/);
  await expect(page.getByTestId("search-note")).toContainText("Searching /My Resources and the folders in it.");
  await expect(page.getByTestId("resources-empty")).toContainText("No files in /My Resources match “onboarding”.");

  await fileManager(page).getByLabel("This folder only").uncheck();
  await expect(rowNamed(page, "Onboarding Guide").first()).toBeVisible();
});
