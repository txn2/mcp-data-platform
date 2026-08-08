import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for prompt resource attachments (#1013). Runs against
// MSW, whose attachment handlers are stateful, so attach / reorder / detach are
// exercised as real request-response round trips rather than as rendering.
//
// prompt-010 ("My Weekly Summary") is the current user's own personal prompt,
// seeded with one readable attachment and one broken link, so the editable and
// degraded states are both reachable from a single fixture.

const OWN_PROMPT = "/portal/prompts/prompt-010";

async function openOwnPrompt(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto(OWN_PROMPT);
  await expect(page.getByTestId("prompt-attachments")).toBeVisible();
}

test.describe("Prompt attachments panel", () => {
  test("lists attached material with its scope and origin", async ({ page }) => {
    await openOwnPrompt(page);

    const panel = page.getByTestId("prompt-attachments");
    await expect(panel).toContainText("Attached materials");
    await expect(panel).toContainText("SQL Style Guide");
    await expect(panel).toContainText("global");
    await expect(panel).toContainText("added by j.martinez@example.com");
  });

  test("flags a deleted resource as a broken link the author can clean up", async ({ page }) => {
    await openOwnPrompt(page);

    const broken = page.getByTestId("attachment-broken");
    await expect(broken).toBeVisible();
    await expect(broken).toContainText("Missing resource");
    await expect(broken).toContainText("res-deleted");
  });

  test("attaches a resource through the picker", async ({ page }) => {
    await openOwnPrompt(page);

    await page.getByRole("button", { name: "Attach" }).click();
    const picker = page.getByTestId("attachment-picker");
    await expect(picker).toBeVisible();

    await picker.getByLabel("Search resources").fill("runbook");
    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/prompts/prompt-010/attachments") && r.request().method() === "POST",
      ),
      picker.getByRole("button").filter({ hasText: /Runbook/i }).first().click(),
    ]);
    expect(resp.status()).toBe(200);

    await expect(page.getByTestId("prompt-attachments")).toContainText(/Runbook/i);
  });

  test("detaches a resource", async ({ page }) => {
    await openOwnPrompt(page);

    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/prompts/prompt-010/attachments/") && r.request().method() === "DELETE",
      ),
      page.getByRole("button", { name: "Detach SQL Style Guide" }).click(),
    ]);
    expect(resp.status()).toBe(200);

    await expect(page.getByTestId("prompt-attachments")).not.toContainText("SQL Style Guide");
  });

  test("reorders material, because the authored order is what the agent receives", async ({ page }) => {
    await openOwnPrompt(page);

    const panel = page.getByTestId("prompt-attachments");
    // The seeded order is [SQL Style Guide, broken link]; moving the first down
    // must persist through a PUT rather than only reordering the DOM.
    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/prompts/prompt-010/attachments") && r.request().method() === "PUT",
      ),
      panel.getByRole("button", { name: "Move down" }).first().click(),
    ]);
    expect(resp.status()).toBe(200);

    await expect(panel.getByTestId("attachment-broken")).toBeVisible();
    const rows = panel.locator("li");
    await expect(rows.first()).toContainText("Missing resource");
  });

  test("the picker offers a filtered, already-attached-free candidate list", async ({ page }) => {
    await openOwnPrompt(page);
    await page.getByRole("button", { name: "Attach" }).click();

    const picker = page.getByTestId("attachment-picker");
    await expect(picker).not.toContainText("SQL Style Guide");

    await picker.getByLabel("Search resources").fill("no-such-resource-anywhere");
    await expect(picker).toContainText("No resources match");
  });

  test("marks material outside the caller's scope without describing it", async ({ page }) => {
    await authenticate(page);
    // prompt-003 carries a persona-scoped resource this caller cannot read.
    await page.goto("/portal/prompts/prompt-003");

    const restricted = page.getByTestId("attachment-unreadable");
    await expect(restricted).toBeVisible();
    await expect(restricted).toContainText("Restricted material");
    // The server sends only the id and a flag for these, so no name, size, or
    // description may appear in the row.
    await expect(restricted).not.toContainText("res-restricted");
  });
});

test.describe("Resource dependency view", () => {
  test("the resource detail lists the prompts that attach it", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/resources");
    // The page opens on "My Resources"; the seeded attachment is a global one.
    await page.getByRole("tab", { name: "Global" }).click();

    await page.getByText("SQL Style Guide").first().click();
    const usedBy = page.getByTestId("resource-used-by-prompts");
    await expect(usedBy).toBeVisible();
    await expect(usedBy).toContainText("Attached to 1 prompt");
    await expect(usedBy).toContainText("My Weekly Summary");
    await expect(usedBy).toContainText("Deleting this resource leaves those prompts serving without it.");
  });
});
