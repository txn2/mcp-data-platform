import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";
import { ADMIN_RESOURCES, chooseUpload, fileManager, gotoFolder, searchBox } from "../screenshots/helpers/resources";
import { RESOURCE_POSITIONING } from "../../src/lib/positioning";

// Interactive coverage for the resources positioning copy (#1015). The
// statement is the same string the agent is served through platform_info, so
// these tests import it rather than restating it: a change to the wording that
// forgets a surface fails here, and a change that forgets the Go constant fails
// in TestResourcePositioningIsVerbatim.
//
// The empty-folder copy an administrator meets is the drop-zone one: an
// administrator can write to every top-level folder, so the read-only empty
// state that carries the statement (#1872: EmptyFolder in
// src/pages/resources/browser/Chrome.tsx) is not reachable as the mock's
// signed-in user. Global's `templates/drafts` is the folder the mock stores
// with nothing in it.

async function openAdminResources(page: Page, root = "user", path = ""): Promise<void> {
  await authenticate(page);
  await gotoFolder(page, ADMIN_RESOURCES, root, path);
  await expect(fileManager(page).getByRole("button", { name: "Upload", exact: true }).first()).toBeVisible();
}

test.describe("Resources positioning copy", () => {
  test("an empty folder says where an upload will be filed and offers one", async ({ page }) => {
    await openAdminResources(page, "global", "templates/drafts");

    const empty = page.getByTestId("resources-empty");
    await expect(empty).toContainText("This folder is empty");
    await expect(empty).toContainText("They are filed into /Global/templates/drafts");
    await expect(empty.getByRole("button", { name: "Upload" })).toBeVisible();
    await expect(page.getByTestId("status-line")).toContainText("0 files");
  });

  test("a search that matches nothing is not reported as an empty folder", async ({ page }) => {
    await openAdminResources(page);
    await searchBox(page).fill("zzz-no-such-resource");

    const empty = page.getByTestId("resources-empty");
    await expect(empty).toContainText("No files in My Resources match");
    await expect(empty).not.toContainText("This folder is empty");
    await expect(empty).not.toContainText(RESOURCE_POSITIONING);
    // Nothing to upload here: the file may well already be in the folder.
    await expect(empty.getByRole("button", { name: "Upload" })).toHaveCount(0);
  });

  test("the upload dialog states the split and what each seed folder means", async ({ page }) => {
    await openAdminResources(page);
    await chooseUpload(page, "A file...");

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
