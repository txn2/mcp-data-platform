import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Asset status (#2502), the Verified badge (#2503) and links between assets
// (#2432), against the MSW mock library.
//
// Links: an author links another asset with mcp:asset:<id>. In a Markdown
// asset it is a plain link with the author's text; in an HTML asset the frame
// (sandbox unchanged) posts the click to the portal, which follows it only to
// an id the asset's content links. A target the reader cannot open shows the
// request-access page. The mock library's own assets are rewritten in place:
// the content is PUT through the mock API from the page, and the asset page is
// reached by a client-side navigation so the in-memory mock keeps the body.

/** An asset the mock reader cannot open (mocks/handlers/assetMarks.ts). */
const LOCKED = "ast-locked";

async function putContent(page: Page, id: string, content: string) {
  const status = await page.evaluate(
    async ([url, body]) => (await fetch(url!, { method: "PUT", body })).status,
    [`/api/v1/portal/assets/${id}/content`, content] as const,
  );
  expect(status).toBe(200);
}

async function goTo(page: Page, path: string) {
  await page.evaluate((p) => {
    history.pushState({}, "", p);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, path);
}

test.describe("status and Verified", () => {
  test("a Deprecated asset shows its note to the reader, with the Status menu for its owner", async ({ page }) => {
    await authenticate(page);
    await goTo(page, "/portal/assets/ast-004");
    const banner = page.getByTestId("asset-deprecated-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toContainText("Replaced by the KPI scorecard dashboard");
    await expect(page.getByTestId("asset-status-badge").first()).toHaveText("Deprecated");
    await expect(page.getByTestId("asset-status-menu")).toBeVisible();
  });

  test("an owner sets a status, and the badge follows", async ({ page }) => {
    await authenticate(page);
    await goTo(page, "/portal/assets/ast-003");
    await expect(page.getByTestId("asset-status-menu")).toBeVisible();
    await expect(page.getByTestId("asset-status-badge")).toHaveCount(0);
    await page.getByTestId("asset-status-menu").click();
    await expect(page.getByText("Status changes no access.")).toBeVisible();
    await page.getByRole("menuitem", { name: /Draft/ }).click();
    await expect(page.getByTestId("asset-status-badge").first()).toHaveText("Draft");
  });

  test("a verified asset with a newer version is marked, and an administrator sees the Verified menu", async ({ page }) => {
    await authenticate(page);
    await goTo(page, "/portal/assets/ast-deck");
    await expect(page.getByTestId("asset-verified-badge").first()).toBeVisible();
    await expect(page.getByTestId("asset-changed-since-verified")).toBeVisible();
    await expect(page.getByTestId("asset-changed-since-verified-note")).toContainText("Verified version 1");
    await expect(page.getByTestId("asset-verified-menu")).toBeVisible();
  });

  // The mock session is an administrator, who may change any asset's status,
  // so a viewer's missing Status menu is held by the component tests
  // (src/components/assetmarks/assetmarks.test.tsx) and, through the real API,
  // by test/acceptance/issue_2502_test.go (a viewer's change answers 403).
  test("an administrator verifies an unverified asset", async ({ page }) => {
    await authenticate(page);
    await goTo(page, "/portal/assets/ast-002");
    await page.getByTestId("asset-verify").click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("textbox", { name: "Note (optional)" }).fill("Checked against the pipeline export.");
    await dialog.getByRole("button", { name: "Verify" }).click();
    await expect(page.getByTestId("asset-verified-badge").first()).toBeVisible();
  });

  test("the Assets list filters by status", async ({ page }) => {
    await authenticate(page);
    await goTo(page, "/portal/assets");
    await page.getByRole("combobox", { name: "Filter by status" }).click();
    await page.getByRole("option", { name: "Deprecated" }).click();
    await expect(page.getByText("KPI Scorecard Component").first()).toBeVisible();
    const badges = page.getByTestId("asset-status-badge");
    await expect(badges.first()).toHaveText("Deprecated");
    expect(new Set(await badges.allTextContents())).toEqual(new Set(["Deprecated"]));
  });
});

test.describe("links between assets", () => {
  test("a Markdown link opens the linked asset, and one the reader cannot open shows the request-access page", async ({ page }) => {
    await authenticate(page);
    await putContent(page, "ast-003", `# Guide\n\nSee [the regional review](mcp:asset:ast-deck).\n\nAnd [the locked one](mcp:asset:${LOCKED}).\n`);
    await goTo(page, "/portal/assets/ast-003");
    const link = page.getByRole("link", { name: "the regional review" });
    await expect(link).toHaveAttribute("href", "/portal/assets/ast-deck");
    // Nothing about the target is shown before the click.
    await expect(page.getByText("Q3 Regional Review")).toHaveCount(0);
    await link.click();
    await expect(page).toHaveURL(/\/portal\/assets\/ast-deck$/);
    await expect(page.getByText("Q3 Regional Review").first()).toBeVisible();

    await goTo(page, "/portal/assets/ast-003");
    await page.getByRole("link", { name: "the locked one" }).click();
    await expect(page).toHaveURL(new RegExp(`/portal/assets/${LOCKED}$`));
    await expect(page.getByText("Owned by sam@example.com")).toBeVisible();
    await expect(page.getByRole("button", { name: /Request access/ })).toBeVisible();
  });

  test("a link in an HTML asset's frame opens the linked asset; an id the content does not link is ignored", async ({ page }) => {
    await authenticate(page);
    await putContent(
      page,
      "ast-001",
      `<!DOCTYPE html><html><head><title>Links</title></head><body>
<p><a id="regional" href="mcp:asset:ast-deck">Regional detail</a></p>
<p><a id="locked" href="mcp:asset:${LOCKED}">Locked detail</a></p>
<script>parent.postMessage({ type: "mcp-asset-link", id: "ast-002" }, "*");</script>
</body></html>`,
    );
    await goTo(page, "/portal/assets/ast-001");
    const frame = page.frameLocator('iframe[title="HTML Preview"]');
    await expect(frame.locator("#regional")).toBeVisible();
    // The forged message names an id the content does not link: the page stays.
    await expect(page).toHaveURL(/\/portal\/assets\/ast-001$/);

    await frame.locator("#regional").click();
    await expect(page).toHaveURL(/\/portal\/assets\/ast-deck$/);
    await expect(page.getByText("Q3 Regional Review").first()).toBeVisible();

    await goTo(page, "/portal/assets/ast-001");
    await frame.locator("#locked").click();
    await expect(page).toHaveURL(new RegExp(`/portal/assets/${LOCKED}$`));
    await expect(page.getByText("Owned by sam@example.com")).toBeVisible();
  });

  test("the linked asset lists the asset linking to it under Linked from", async ({ page }) => {
    await authenticate(page);
    await putContent(page, "ast-003", `See [the regional review](mcp:asset:ast-deck).`);
    await goTo(page, "/portal/assets/ast-deck");
    await page.getByRole("button", { name: "Show details" }).click();
    const linkedFrom = page.getByTestId("asset-linked-from");
    await expect(linkedFrom).toBeVisible();
    await expect(linkedFrom).toContainText("Weekly Inventory Report");
  });
});
