import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";
import { RESOURCE_POSITIONING } from "../../src/lib/positioning";

// Interactive coverage for the resources positioning copy (#1015). The
// statement is the same string the agent is served through platform_info, so
// these tests import it rather than restating it: a change to the wording that
// forgets a surface fails here, and a change that forgets the Go constant fails
// in TestResourcePositioningIsVerbatim.
//
// The "admin" persona is the empty scope in the mock fixture (every other
// persona owns at least one resource), which is what gives the never-uploaded
// empty state something to render.

const ADMIN_RESOURCES = "/portal/admin/resources";

async function openAdminResources(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto(ADMIN_RESOURCES);
  await expect(page.getByRole("button", { name: "Upload" })).toBeVisible();
}

test.describe("Resources positioning copy", () => {
  test("the empty scope states what a resource is for", async ({ page }) => {
    await openAdminResources(page);
    await page.getByRole("tab", { name: "admin", exact: true }).click();

    const empty = page.getByTestId("resources-empty");
    await expect(empty).toContainText("No resources yet");
    await expect(empty).toContainText(RESOURCE_POSITIONING);
    await expect(empty.getByRole("button", { name: "Upload Resource" })).toBeVisible();
  });

  test("a filter that matches nothing is not reported as an empty library", async ({ page }) => {
    await openAdminResources(page);
    await page.getByPlaceholder("Search resources...").fill("zzz-no-such-resource");

    const empty = page.getByTestId("resources-empty");
    await expect(empty).toContainText("No resources match this search");
    await expect(empty).not.toContainText(RESOURCE_POSITIONING);
    // Nothing to upload here: the file may well already be in the library.
    await expect(empty.getByRole("button", { name: "Upload Resource" })).toHaveCount(0);
  });

  test("the upload dialog states the split and what each category means", async ({ page }) => {
    await openAdminResources(page);
    await page.getByRole("button", { name: "Upload" }).click();

    const dialog = page.getByRole("heading", { name: "Upload Resource" }).locator("../..");
    await expect(dialog).toContainText(RESOURCE_POSITIONING);

    // The hint tracks the selected category, so the meaning is in front of the
    // person at the moment they choose.
    const hint = page.getByTestId("category-hint");
    await expect(hint).toHaveText("Example payloads and extracts the agent can pattern-match against.");

    // The category chooser is a Radix listbox, not a native <select>: an option
    // is picked by opening the trigger and clicking it, and the open list is
    // portalled out of the dialog, so the option query is page-scoped.
    const category = dialog.getByRole("combobox", { name: "Category" });
    await category.click();
    await page.getByRole("option", { name: "templates" }).click();
    await expect(hint).toHaveText("Layouts a deliverable must be produced in, used verbatim.");

    await category.click();
    await page.getByRole("option", { name: "references" }).click();
    await expect(hint).toHaveText("Data dictionaries, standards, and background documents to consult.");

    // A custom category has no built-in meaning to state.
    await category.click();
    await page.getByRole("option", { name: "Custom..." }).click();
    await expect(page.getByTestId("category-hint")).toHaveCount(0);
  });
});

// The asset and knowledge-page cross-references live in empty states the mock
// fixture cannot reach (both surfaces own data), so they are covered by
// component tests that render the empty state directly:
// MyAssetsPage.test.tsx and KnowledgePageList.test.tsx.
