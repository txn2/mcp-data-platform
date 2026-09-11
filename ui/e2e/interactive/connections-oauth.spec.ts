import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The connection editor's OAuth block, driven through the assembled portal
// rather than through its props.
//
// Migration 000050 rewrote every api-kind OAuth connection onto auth_mode
// "oauth" plus oauth_grant, and this editor kept offering only the two legacy
// modes and reading only the legacy keys. An OAuth connection therefore opened
// with no auth configuration at all: the mode select matched nothing and the
// whole OAuth block was skipped, which is exactly what a component test with
// hand-built props cannot show (#1681). A second connection carries both
// vocabularies, which the viewer has to mark rather than list as equals
// (#1682).

async function openConnection(page: Page, name: string): Promise<void> {
  await authenticate(page);
  await page.goto(`/portal/admin/connections?kind=api&name=${name}`);
  await expect(page.getByRole("heading", { name, level: 2 })).toBeVisible();
}

test.describe("Connection editor — a canonical OAuth connection", () => {
  test("opens with the OAuth block populated from the stored keys", async ({
    page,
  }) => {
    await openConnection(page, "acme-billing-api");
    await page.getByRole("button", { name: "Edit" }).first().click();

    await expect(page.getByLabel("Auth mode")).toHaveText("OAuth 2.1");
    await expect(page.getByLabel("Grant type")).toHaveText(/authorization_code/);
    await expect(page.getByLabel("Token URL")).toHaveValue(
      "https://auth.acme.example.com/oauth2/token",
    );
    await expect(page.getByLabel("Authorization URL")).toHaveValue(
      "https://auth.acme.example.com/oauth2/authorize",
    );
    await expect(page.getByLabel("Client ID")).toHaveValue("acme-billing-api");
    await expect(page.getByLabel("Client Secret")).toHaveValue("[REDACTED]");
    await expect(page.getByLabel("Scope")).toHaveValue("billing.read");
    await expect(
      page.getByRole("button", { name: "Connect", exact: true }),
    ).toBeVisible();
  });
});

test.describe("Connection viewer — both OAuth vocabularies on one connection", () => {
  test("marks the keys nothing reads and names the vocabulary in use", async ({
    page,
  }) => {
    await openConnection(page, "acme-analytics-api");

    await expect(
      page.getByText(/both OAuth configuration vocabularies/i),
    ).toBeVisible();
    // One per legacy key the canonical sibling overrides: the client id, the
    // secret and the scopes.
    await expect(page.getByText("shadowed")).toHaveCount(3);
    await expect(
      page.locator("span.line-through", { hasText: "acme-analytics-api" }),
    ).toBeVisible();
    // The value it is live on is not struck through.
    await expect(
      page.locator("span.line-through", { hasText: "PENDING-REPLACE-ME" }),
    ).toHaveCount(0);
    // The OAuth status card says the same thing where an operator triaging a
    // failing connection looks first.
    await expect(
      page.getByText(/Two OAuth configurations on one connection/i),
    ).toBeVisible();
  });
});
