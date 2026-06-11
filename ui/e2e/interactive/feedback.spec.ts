import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the feedback thread UI. Runs against MSW with seeded
// threads on ast-001 (asset) and a standalone channel. Exercises: opening the
// per-asset drawer, creating a thread (asserting the POST), replying, changing
// status, and the standalone channel page.

// The asset-viewer toolbar Feedback button (scoped to main so it never matches
// the sidebar "Feedback" nav entry).
function toolbarFeedback(page: Page) {
  return page.getByRole("main").getByRole("button", { name: /Feedback/ }).first();
}

async function openAssetFeedback(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/assets/ast-001");
  await toolbarFeedback(page).click();
  await expect(page.getByRole("heading", { name: "Asset feedback" })).toBeVisible();
}

test.describe("Feedback panel", () => {
  test("opens the drawer and lists seeded threads", async ({ page }) => {
    await openAssetFeedback(page);
    await expect(page.getByText("We don't use that term")).toBeVisible();
  });

  test("creates a new thread", async ({ page }) => {
    await openAssetFeedback(page);
    await page.getByRole("button", { name: "New", exact: true }).click();
    await expect(page.getByRole("heading", { name: "New feedback" })).toBeVisible();

    await page.getByPlaceholder("Describe your feedback").fill("The y-axis label is misspelled.");

    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/threads") && r.request().method() === "POST",
      ),
      page.getByRole("button", { name: /Post feedback/ }).click(),
    ]);
    expect(resp.status()).toBe(201);

    // Lands on the new thread's detail with the posted message in the timeline.
    await expect(page.getByText("The y-axis label is misspelled.")).toBeVisible();
  });

  test("opens a thread, replies, and changes status", async ({ page }) => {
    await openAssetFeedback(page);
    await page.getByText("We don't use that term").click();

    // Timeline of the seeded correction is visible.
    await expect(page.getByText(/active practitioners/)).toBeVisible();

    await page.getByPlaceholder("Reply…").fill("Fixed in section 2 as well.");
    await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/events") && r.request().method() === "POST",
      ),
      page.getByRole("button", { name: "Reply", exact: true }).click(),
    ]);
    await expect(page.getByText("Fixed in section 2 as well.")).toBeVisible();

    // Moderator status control (mock user is admin).
    await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/threads/") && r.request().method() === "PATCH",
      ),
      page.getByRole("combobox").selectOption("resolved"),
    ]);
  });

  test("standalone channel page lists general feedback", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/feedback");
    await expect(
      page.getByText("General feedback and suggestions, visible to everyone"),
    ).toBeVisible();
    await expect(page.getByText("Quarterly data refresh is one day late")).toBeVisible();
  });
});
