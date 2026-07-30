import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the prompt library reorganization (#1010). Runs
// against MSW with the seeded prompt/collection/usage/version fixtures.
// Exercises: the two-bucket model (My Prompts / Library), collection grouping,
// facets, usage sort, the collections manager, and the viewer's version
// history, diff, invocation help, and collection assignment.

async function openPrompts(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/prompts");
  await expect(page.getByRole("button", { name: /My Prompts/ })).toBeVisible();
}

async function openLibraryTab(page: Page): Promise<void> {
  await openPrompts(page);
  await page.getByRole("button", { name: /^Library/ }).click();
  await expect(page.getByRole("heading", { name: "Sales Reporting" })).toBeVisible();
}

test.describe("Prompt library buckets", () => {
  test("My Prompts merges personal and shared prompts with attribution", async ({ page }) => {
    await openPrompts(page);

    // A personal prompt and a shared prompt appear in one bucket.
    await expect(page.getByText("My Weekly Summary")).toBeVisible();
    await expect(page.getByText("Regional Deep Dive")).toBeVisible();
    await expect(page.getByText("Shared by carol@example.com")).toBeVisible();

    // Usage columns are populated from the rollup.
    await expect(page.getByRole("main")).toContainText("Runs");
    await expect(page.getByRole("main")).toContainText("Last run");
  });

  test("the scope taxonomy is not shown in the library", async ({ page }) => {
    await openPrompts(page);
    const main = page.getByRole("main");
    await expect(main).not.toContainText(/\bScope\b/);
    await expect(main).not.toContainText(/\bPersona\b/);

    await page.getByRole("button", { name: /^Library/ }).click();
    await expect(main).not.toContainText(/\bScope\b/);
    await expect(main).not.toContainText(/\bPersona\b/);
  });

  test("Library groups prompts by collection with a trailing default group", async ({ page }) => {
    await openLibraryTab(page);

    await expect(page.getByRole("heading", { name: "Sales Reporting" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Data Operations" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Executive Briefings" })).toBeVisible();
    // Uncollected prompts land in General.
    await expect(page.getByRole("heading", { name: "General" })).toBeVisible();
    await expect(page.getByText("Inventory Health Check")).toBeVisible();
  });

  test("dead prompts are visually identifiable", async ({ page }) => {
    await openLibraryTab(page);
    // prompt-008 (Stock Level Alert) has no usage entry and is 25 days old:
    // flagged with the exact condition it is in (#1124), not a
    // lifecycle-sounding "inactive".
    const row = page.getByRole("row", { name: /Stock Level Alert/ });
    await expect(row.getByText("never run")).toBeVisible();
    await expect(row.getByText("Never", { exact: true })).toBeVisible();
  });
});

test.describe("Prompt library facets and sorting", () => {
  test("collection facet narrows the list and clears", async ({ page }) => {
    await openLibraryTab(page);

    await page.getByLabel("Filter by collection").selectOption({ label: "Data Operations" });
    await expect(page.getByText("Data Quality Scan", { exact: true })).toBeVisible();
    await expect(page.getByText("Daily Sales Report", { exact: true })).not.toBeVisible();

    await page.getByRole("button", { name: /Clear filters/ }).click();
    await expect(page.getByText("Daily Sales Report", { exact: true })).toBeVisible();
  });

  test("usage facet isolates inactive prompts", async ({ page }) => {
    await openLibraryTab(page);

    await page.getByLabel("Filter by usage").selectOption("inactive");
    await expect(page.getByText("Stock Level Alert", { exact: true })).toBeVisible();
    await expect(page.getByText("Daily Sales Report", { exact: true })).not.toBeVisible();
  });

  test("an over-narrow facet combination shows the filtered empty state", async ({ page }) => {
    await openLibraryTab(page);

    await page.getByLabel("Filter by collection").selectOption({ label: "Executive Briefings" });
    await page.getByLabel("Filter by usage").selectOption("inactive");
    await expect(page.getByText("No prompts match the current filters")).toBeVisible();
  });

  test("run-count sort surfaces the most active prompts first", async ({ page }) => {
    await openLibraryTab(page);

    // Sorting by Runs defaults to descending; within the Sales Reporting
    // group the daily report (128 runs) must precede the revenue forecast (23).
    await page.getByRole("columnheader", { name: "Runs" }).first().click();
    const salesTable = page.getByRole("table").filter({ hasText: "Revenue Forecast" });
    const first = salesTable.getByRole("row").nth(1);
    await expect(first).toContainText("Daily Sales Report");
    await expect(first).toContainText("128");
  });

  test("search ranks across buckets and includes shared matches", async ({ page }) => {
    await openPrompts(page);

    await page.getByPlaceholder("Search prompts by meaning...").fill("regional");
    await expect(page.getByText(/Ranked by relevance/)).toBeVisible();
    await expect(page.getByText("Regional Deep Dive")).toBeVisible();
    await expect(page.getByText("Shared by carol@example.com")).toBeVisible();
  });
});

test.describe("Collections manager", () => {
  test("creates, renames, and deletes a collection", async ({ page }) => {
    await openPrompts(page);
    await page.getByRole("button", { name: "Collections", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "Manage collections" })).toBeVisible();

    // Create.
    await page.getByPlaceholder("Collection name").fill("Churn Playbooks");
    const [createResp] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/prompt-collections") && r.request().method() === "POST"),
      page.getByRole("button", { name: "Create" }).click(),
    ]);
    expect(createResp.status()).toBe(201);
    const dialog = page.getByRole("dialog", { name: "Manage collections" });
    await expect(dialog.getByText("Churn Playbooks")).toBeVisible();

    // Duplicate names are rejected with the server's message.
    await page.getByPlaceholder("Collection name").fill("churn playbooks");
    await page.getByRole("button", { name: "Create" }).click();
    await expect(page.getByText(/already exists/)).toBeVisible();

    // Rename.
    await page.getByRole("button", { name: "Rename Churn Playbooks" }).click();
    await page.getByPlaceholder("Collection name").fill("Retention Playbooks");
    const [renameResp] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/prompt-collections/") && r.request().method() === "PUT"),
      page.getByRole("button", { name: "Save", exact: true }).click(),
    ]);
    expect(renameResp.status()).toBe(200);
    await expect(dialog.getByText("Retention Playbooks")).toBeVisible();

    // Delete.
    const [deleteResp] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/prompt-collections/") && r.request().method() === "DELETE"),
      page.getByRole("button", { name: "Delete Retention Playbooks" }).click(),
    ]);
    expect(deleteResp.status()).toBe(200);
    await expect(dialog.getByText("Retention Playbooks")).not.toBeVisible();
  });
});

test.describe("Prompt viewer verification surface", () => {
  test("shows invocation help with a copyable bare-name invocation", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/prompts/prompt-003");

    await expect(page.getByText("Run from chat")).toBeVisible();
    await expect(page.getByText(/Run the daily-sales-report prompt/)).toBeVisible();
  });

  test("renders version history with approval provenance and a pending draft", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/prompts/prompt-003");

    await expect(page.getByText("Version history")).toBeVisible();
    await expect(page.getByTestId("pending-draft-banner")).toContainText(
      "Draft v4 by bob@example.com is pending review",
    );
    await expect(page.getByText("approved by alice@example.com").first()).toBeVisible();
    await expect(page.getByText("current", { exact: true })).toBeVisible();
  });

  test("diffs an older version against the served content", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/prompts/prompt-003");

    // v1 is the shortest snapshot; diffing it against current shows additions.
    await page.getByRole("button", { name: "Diff vs current" }).last().click();
    const diff = page.getByTestId("version-diff");
    await expect(diff).toBeVisible();
    await expect(diff).toContainText("v1 → v3 (current)");
    await expect(diff).toContainText("average order value");

    await diff.getByRole("button", { name: "Close diff" }).click();
    await expect(page.getByTestId("version-diff")).not.toBeVisible();
  });

  test("assigns the prompt to a collection from the viewer", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/prompts/prompt-004");

    const picker = page.getByLabel("Collection");
    await expect(picker).toBeVisible();
    const [resp] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/prompts/prompt-004/collection") && r.request().method() === "PUT"),
      picker.selectOption({ label: "Data Operations" }),
    ]);
    expect(resp.status()).toBe(200);
  });

  test("back navigation returns from the viewer to the library", async ({ page }) => {
    await openPrompts(page);
    await page.getByText("My Weekly Summary").click();
    await expect(page.getByText("Run from chat")).toBeVisible();

    await page.getByRole("button", { name: "Back", exact: true }).click();
    await expect(page.getByRole("button", { name: /My Prompts/ })).toBeVisible();
  });
});
