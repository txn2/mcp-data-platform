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
    // The picker is one listbox now (#1553).
    await page.getByRole("combobox", { name: "Library" }).click();
    await page.getByRole("option", { name: "admin", exact: true }).click();

    const empty = page.getByTestId("resources-empty");
    await expect(empty).toContainText("Nothing here yet");
    await expect(empty).toContainText(RESOURCE_POSITIONING);
    await expect(empty.getByRole("button", { name: "Upload Resource" })).toBeVisible();
  });

  test("a filter that matches nothing is not reported as an empty library", async ({ page }) => {
    await openAdminResources(page);
    await page.getByPlaceholder("Search the whole library...").fill("zzz-no-such-resource");

    const empty = page.getByTestId("resources-empty");
    await expect(empty).toContainText("No resources match this search");
    await expect(empty).not.toContainText(RESOURCE_POSITIONING);
    // Nothing to upload here: the file may well already be in the library.
    await expect(empty.getByRole("button", { name: "Upload Resource" })).toHaveCount(0);
  });

  test("the upload dialog states the split and what each seed folder means", async ({ page }) => {
    await openAdminResources(page);
    await page.getByRole("button", { name: "Upload" }).click();

    const dialog = page.getByRole("heading", { name: "Upload Resource" }).locator("../..");
    await expect(dialog).toContainText(RESOURCE_POSITIONING);

    // The hint tracks the folder chosen, so the meaning is in front of the
    // person at the moment they choose.
    const hint = page.getByTestId("path-hint");
    await expect(hint).toHaveText("Example payloads and extracts the agent can pattern-match against.");

    await dialog.getByRole("combobox", { name: "Folder" }).click();
    await page.getByRole("option", { name: "templates", exact: true }).click();
    await expect(hint).toHaveText("Layouts a deliverable must be produced in, used verbatim.");

    // A folder that does not exist yet is typed, and the hint is read off the
    // FIRST segment, so a path nested under a seed folder keeps saying what
    // that folder is for (#1553: the control is a listbox until it is told to
    // take a new name).
    await dialog.getByRole("combobox", { name: "Folder" }).click();
    await page.getByRole("option", { name: "New folder..." }).click();
    const folder = dialog.getByLabel("Folder");

    await folder.fill("references/glossary/terms");
    await expect(hint).toHaveText("Data dictionaries, standards, and background documents to consult.");

    // A folder the platform suggests nothing about has no meaning to state.
    await folder.fill("media-manager");
    await expect(page.getByTestId("path-hint")).toHaveCount(0);

    // And a path that breaks a rule says which rule, in the hint's place.
    await folder.fill("Media-Manager");
    await expect(page.getByTestId("path-problem")).toContainText("must be lowercase");
  });
});

// The asset and knowledge-page cross-references live in empty states the mock
// fixture cannot reach (both surfaces own data), so they are covered by
// component tests that render the empty state directly:
// MyAssetsPage.test.tsx and KnowledgePageList.test.tsx.
