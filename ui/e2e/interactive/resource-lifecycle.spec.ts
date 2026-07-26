import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the managed-resource lifecycle surfaces (#1014):
// version history with replace and restore, the usage panel, and the admin
// table's last-read column and ordering. Runs against MSW, whose resource
// handlers answer the version, replace, restore, and version-download routes,
// so each action is exercised as a real request-response round trip.
//
// res-001 ("SQL Style Guide") is the fixture with a three-revision trail (the
// newest a restore of v1) and read activity; res-002 has neither, which is what
// gives the never-read state something to render.

const ADMIN_RESOURCES = "/portal/admin/resources";

async function openResourceDetail(page: Page, name: string): Promise<void> {
  await authenticate(page);
  await page.goto(ADMIN_RESOURCES);
  await page.getByText(name, { exact: true }).first().click();
  await expect(page.getByTestId("resource-versions")).toBeVisible();
}

test.describe("Resource version history", () => {
  test("lists every revision with its uploader, and marks the current one", async ({ page }) => {
    await openResourceDetail(page, "SQL Style Guide");

    const panel = page.getByTestId("resource-versions");
    await expect(panel).toContainText("Version history");
    await expect(panel).toContainText("3 of 10 kept");

    const head = page.getByTestId("resource-version-3");
    await expect(head).toContainText("current");
    await expect(head).toContainText("sarah.chen@example.com");
    // A restore is recorded as a new head revision naming what it re-promoted.
    await expect(head).toContainText("restored v1");

    await expect(page.getByTestId("resource-version-1")).toContainText("sarah.chen@example.com");
  });

  test("the current revision offers no restore, prior ones do", async ({ page }) => {
    await openResourceDetail(page, "SQL Style Guide");

    await expect(page.getByTestId("restore-version-3")).toHaveCount(0);
    await expect(page.getByTestId("restore-version-1")).toBeVisible();
  });

  test("replacing content posts to the resource that already exists", async ({ page }) => {
    await openResourceDetail(page, "SQL Style Guide");

    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/api/v1/resources/res-001/content") && r.request().method() === "POST",
      ),
      page.getByTestId("replace-content-input").setInputFiles({
        name: "renamed-by-the-user.pdf",
        mimeType: "application/pdf",
        buffer: Buffer.from("%PDF-1.4 revised"),
      }),
    ]);

    expect(resp.status()).toBe(200);
    // The identity is what must survive a revision: same id, same URI.
    const body = (await resp.json()) as { id: string; uri: string };
    expect(body.id).toBe("res-001");
    expect(body.uri).toContain("sql-style-guide.pdf");
  });

  test("restoring a prior revision posts the restore", async ({ page }) => {
    await openResourceDetail(page, "SQL Style Guide");

    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) =>
          r.url().includes("/api/v1/resources/res-001/versions/1/restore") &&
          r.request().method() === "POST",
      ),
      page.getByTestId("restore-version-1").click(),
    ]);
    expect(resp.status()).toBe(200);
  });

  test("a prior revision's content can be downloaded", async ({ page }) => {
    await openResourceDetail(page, "SQL Style Guide");

    const [resp] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/api/v1/resources/res-001/versions/1/content")),
      page.getByTestId("download-version-1").click(),
    ]);
    expect(resp.status()).toBe(200);
  });

  test("a resource with no recorded revisions says so", async ({ page }) => {
    await openResourceDetail(page, "Data Dictionary");
    await expect(page.getByTestId("resource-versions")).toContainText("No revisions recorded yet");
  });
});

test.describe("Resource usage", () => {
  test("shows read counts broken down by surface", async ({ page }) => {
    await openResourceDetail(page, "SQL Style Guide");

    const usage = page.getByTestId("resource-usage");
    await expect(usage).toBeVisible();
    await expect(page.getByTestId("usage-reads-30d")).toHaveText("46");
    await expect(page.getByTestId("usage-reads-90d")).toHaveText("118");
    await expect(usage).toContainText("Agent read");
    await expect(usage).toContainText("Search fetch");
    await expect(usage).toContainText("Portal download");
  });

  test("a resource with no read activity renders no usage panel", async ({ page }) => {
    await openResourceDetail(page, "Data Dictionary");
    await expect(page.getByTestId("resource-usage")).toHaveCount(0);
  });
});

test.describe("Admin resources table", () => {
  test("shows last-read recency, flagging what has never been read", async ({ page }) => {
    await authenticate(page);
    await page.goto(ADMIN_RESOURCES);

    await expect(page.getByRole("columnheader", { name: "Last read" })).toBeVisible();
    // res-001 has read activity; res-002 has none and is old enough to flag.
    await expect(page.getByTestId("resource-last-read-res-001")).not.toHaveText("Never");
    await expect(page.getByTestId("resource-last-read-res-002")).toHaveText("Never");
  });

  test("sorting by recently read asks the server for that order", async ({ page }) => {
    await authenticate(page);
    await page.goto(ADMIN_RESOURCES);

    const [resp] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("sort=last_read")),
      page.getByTestId("resources-sort").selectOption("last_read"),
    ]);
    expect(resp.status()).toBe(200);

    // The most recently read resource leads the table.
    const firstRow = page.locator("tbody tr").first();
    await expect(firstRow).toContainText("SQL Style Guide");
  });
});
