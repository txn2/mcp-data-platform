import { test, expect, type Page } from "@playwright/test";

// #2242: portal.login_redirect sends a signed-out portal visitor straight to
// the identity provider. Each case names the acceptance criterion it is; the
// Go criterion that runs it supplies the environment below.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; this suite is run by test/acceptance/issue_2242_test.go`);
  return v;
};

// Where Keycloak's login form is served from: its authorization endpoint.
const IDP_LOGIN = /\/realms\/[^/]+\/protocol\/openid-connect\/auth/;

/**
 * Records whether the portal ever drew its login screen on this page: the API
 * key field or the SSO button. The observer runs in every document the page
 * loads and reports through a binding, so a screen drawn and navigated away
 * from within one frame is still seen.
 */
async function watchLoginScreen(page: Page): Promise<{ seen: boolean }> {
  const state = { seen: false };
  await page.exposeBinding("__loginScreenDrawn", () => {
    state.seen = true;
  });
  await page.addInitScript(() => {
    const report = () => (window as unknown as { __loginScreenDrawn: () => void }).__loginScreenDrawn();
    const check = () => {
      if (document.querySelector('input[placeholder="X-API-Key"]')) report();
      for (const b of document.querySelectorAll("button")) {
        if (/^Sign in with/.test(b.textContent?.trim() ?? "")) report();
      }
    };
    new MutationObserver(check).observe(document, { childList: true, subtree: true });
  });
  return state;
}

/** Signs in on Keycloak's own form. */
async function signInAtIdP(page: Page) {
  await page.locator("#username").fill(env("LOGIN_REDIRECT_IDP_USER"));
  await page.locator("#password").fill(env("LOGIN_REDIRECT_IDP_PASSWORD"));
  await page.locator("#kc-login").click();
}

test("criterion 1: a signed-out asset page goes to the identity provider and comes back to the asset", async ({ page }) => {
  const asset = `/portal/assets/${env("LOGIN_REDIRECT_ASSET_ID")}`;
  const screen = await watchLoginScreen(page);

  await page.goto(asset);
  await page.waitForURL(IDP_LOGIN);
  expect(screen.seen, "the portal login screen was drawn before the identity provider").toBe(false);

  await signInAtIdP(page);
  await page.waitForURL((u) => u.pathname === asset);
  await expect(page.locator('input[placeholder="X-API-Key"]')).toHaveCount(0);
});

test("criterion 2: an expired session goes to the identity provider", async ({ page, context }) => {
  const base = new URL(env("LOGIN_REDIRECT_BASE_URL"));
  await context.addCookies([
    {
      name: env("LOGIN_REDIRECT_COOKIE_NAME"),
      value: env("LOGIN_REDIRECT_EXPIRED_SESSION"),
      domain: base.hostname,
      path: "/",
    },
  ]);
  const screen = await watchLoginScreen(page);

  const me = page.waitForResponse((r) => r.url().endsWith("/api/v1/portal/me"));
  await page.goto("/portal/");
  expect((await me).status(), "the expired session was accepted").toBe(401);
  await page.waitForURL(IDP_LOGIN);
  expect(screen.seen, "the portal login screen was drawn for an expired session").toBe(false);
});

test("criterion 3: a refused sign-in shows the error and redirects only when asked", async ({ page }) => {
  const logins: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).pathname === "/portal/auth/login") logins.push(r.url());
  });

  await page.goto("/portal/?error=access_denied");
  await expect(page.getByText("Access was denied by the identity provider.")).toBeVisible();
  const again = page.getByRole("button", { name: "Try again" });
  await expect(again).toBeVisible();
  await expect(page.locator('input[placeholder="X-API-Key"]')).toHaveCount(0);
  // Long enough for a redirect the page would make on its own to have started.
  await page.waitForLoadState("networkidle");
  expect(logins, "the portal went back to the identity provider unasked").toEqual([]);

  await again.click();
  await page.waitForURL(IDP_LOGIN);
  expect(logins).toHaveLength(1);
});

test("criterion 4: ?login=api_key offers the key form and a valid key signs in", async ({ page }) => {
  await page.goto("/portal/?login=api_key");
  const field = page.locator('input[placeholder="X-API-Key"]');
  await expect(field).toBeVisible();
  expect(IDP_LOGIN.test(page.url())).toBe(false);

  await field.fill(env("LOGIN_REDIRECT_API_KEY"));
  const me = page.waitForResponse((r) => r.url().endsWith("/api/v1/portal/me") && r.request().headers()["x-api-key"] !== undefined);
  await page.getByRole("button", { name: "Sign in with API key" }).click();
  expect((await me).status()).toBe(200);
  await expect(field).toHaveCount(0);
  expect(new URL(page.url()).pathname.startsWith("/portal")).toBe(true);
});

test("criterion 5: ?login=api_key goes to the identity provider when the key form is off", async ({ page }) => {
  const screen = await watchLoginScreen(page);
  await page.goto("/portal/?login=api_key");
  await page.waitForURL(IDP_LOGIN);
  expect(screen.seen, "the key form was drawn although allow_api_key_login is false").toBe(false);
});

test("criterion 6: with the setting off the login screen is unchanged", async ({ page }) => {
  await page.goto(`/portal/assets/${env("LOGIN_REDIRECT_ASSET_ID")}`);
  await expect(page.getByRole("button", { name: /^Sign in with/ }).first()).toBeVisible();
  await expect(page.getByText("or use an API key")).toBeVisible();
  await expect(page.locator('input[placeholder="X-API-Key"]')).toBeVisible();
  expect(IDP_LOGIN.test(page.url())).toBe(false);
});
