import { type Page } from "@playwright/test";

export async function authenticate(page: Page): Promise<void> {
  await page.goto("/portal/");

  const nav = page.locator("nav");
  const apiKeyInput = page.locator('input[placeholder="API Key"]');

  const which = await Promise.race([
    nav.waitFor({ state: "visible", timeout: 20_000 }).then(() => "nav" as const),
    apiKeyInput
      .waitFor({ state: "visible", timeout: 20_000 })
      .then(() => "login" as const),
  ]);

  if (which === "login") {
    await apiKeyInput.fill("mock-screenshot-key");
    const signInButton = page.locator(
      'button:has-text("Sign In with API Key")',
    );
    await signInButton.waitFor({ state: "visible", timeout: 5_000 });
    await signInButton.click();
    await nav.waitFor({ state: "visible", timeout: 15_000 });
  }
}
