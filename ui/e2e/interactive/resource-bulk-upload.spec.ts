import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";
import { ADMIN_RESOURCES, chooseUpload, gotoFolder } from "../screenshots/helpers/resources";

// The many-files upload (#1862), driven against MSW, whose create handler
// answers if_exists=skip_unchanged the way the server does: the same bytes at
// an address are left alone, different bytes become a new version, and an
// empty address is created. The "admin" persona folder is empty in the mock
// fixture, so every file this spec uploads is its own. The upload is started
// from the file manager's Upload menu (#1872), in that top-level folder.

async function openBulkUpload(page: Page): Promise<void> {
  await authenticate(page);
  await gotoFolder(page, ADMIN_RESOURCES, "admin");
  await chooseUpload(page, "Many files or a folder...");
  await expect(page.getByTestId("bulk-empty")).toBeVisible();
}

function png(name: string, body: string) {
  return { name, mimeType: "image/png", buffer: Buffer.from(body) };
}

test.describe("Bulk resource upload", () => {
  test("reports created, new version, unchanged and not sent per file", async ({ page }) => {
    await openBulkUpload(page);
    const input = page.getByTestId("bulk-files-input");

    await input.setInputFiles([png("Mark.png", "mark-v1"), png("Wordmark.png", "word-v1")]);
    await expect(page.getByTestId("bulk-row")).toHaveCount(2);
    await page.getByRole("button", { name: "Upload 2 files" }).click();
    await expect(page.getByTestId("bulk-summary")).toHaveText("2 created, 0 new versions, 0 unchanged, 0 failed");

    await page.getByRole("button", { name: "Clear list" }).click();
    await input.setInputFiles([
      png("Mark.png", "mark-v1"),
      png("Wordmark.png", "word-v2"),
      png("Badge.png", "badge"),
      { name: "setup.exe", mimeType: "application/octet-stream", buffer: Buffer.from("MZ") },
    ]);
    await expect(page.getByTestId("bulk-row")).toHaveCount(4);
    await page.getByRole("button", { name: "Upload 3 files" }).click();

    await expect(page.getByTestId("bulk-summary")).toHaveText(
      "1 created, 1 new version, 1 unchanged, 0 failed, 1 not sent",
    );
    await expect(page.getByTestId("bulk-outcome")).toHaveText(["Unchanged", "New version", "Created"]);
    await expect(page.getByTestId("bulk-failure")).toContainText("Files ending in .exe are not accepted.");
    await expect(page.getByTestId("web-image-badge")).toHaveCount(3);
  });
});
