import { expect, test, type Page } from "@playwright/test";

function env(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; test/acceptance/issue_2579_test.go sets it`);
  return v;
}

const IDP_LOGIN = /\/realms\/[^/]+\/(protocol\/openid-connect\/auth|login-actions)/;

/** violations collects what Chromium logs when a Content Security Policy blocks something. */
function violations(page: Page): string[] {
  const seen: string[] = [];
  page.on("console", (m) => {
    if (/content security policy/i.test(m.text())) seen.push(m.text());
  });
  return seen;
}

async function fillIdpForm(page: Page): Promise<void> {
  await page.waitForURL(IDP_LOGIN);
  await page.locator("#username").fill(env("SITE_SIGNIN_EMAIL"));
  await page.locator("#password").fill(env("SITE_SIGNIN_PASSWORD"));
  await page.locator("#kc-login").click();
}

async function signInToPortal(page: Page): Promise<void> {
  await page.goto("/portal/auth/login");
  await fillIdpForm(page);
  await page.waitForURL(/\/portal\//);
}

test("criterion 1: someone signed in to the portal arrives at the requested path with no click", async ({ page }) => {
  const seen = violations(page);
  await signInToPortal(page);
  const target = `${env("SITE_SIGNIN_OPEN_URL")}/?from=criterion1`;

  await page.goto(target);

  await expect(page.locator("h1")).toHaveText("signin2579");
  expect(page.url()).toBe(target);
  expect(seen, "a policy blocked part of the sign-in").toEqual([]);
});

test("criterion 2: someone not signed in goes through the login and arrives at the requested path", async ({ page }) => {
  const seen = violations(page);
  const target = `${env("SITE_SIGNIN_OPEN_URL")}/?from=criterion2`;

  await page.goto(target);
  await fillIdpForm(page);

  await page.waitForURL(target);
  await expect(page.locator("h1")).toHaveText("login2579");
  expect(seen, "a policy blocked part of the sign-in").toEqual([]);
});

test("criterion 3: someone with no access sees the page and Use another account reaches the portal sign-out", async ({ page }) => {
  const seen = violations(page);
  const logout: number[] = [];
  page.on("response", (r) => {
    if (new URL(r.url()).pathname === "/portal/auth/logout") logout.push(r.status());
  });
  await signInToPortal(page);

  await page.goto(env("SITE_SIGNIN_CLOSED_URL"));
  await expect(page.getByRole("heading", { name: "You don't have access to this site" })).toBeVisible();

  await page.getByRole("button", { name: "Use another account" }).click();

  await expect.poll(() => logout, "the portal's sign-out was reached").toHaveLength(1);
  expect(logout[0], "the portal's sign-out answered").toBeLessThan(400);
  // The sign-out's response can arrive before the navigation it starts is
  // committed, so the page's address is waited for, not read once.
  await expect.poll(() => new URL(page.url()).host, "the page stayed on the site").not.toBe(new URL(env("SITE_SIGNIN_CLOSED_URL")).host);
  expect(seen, "a policy blocked the sign-out").toEqual([]);
});

test("criterion 4: the Sign in link on the page a form post gets signs in", async ({ page, context }) => {
  const seen = violations(page);
  await signInToPortal(page);
  const site = env("SITE_SIGNIN_OPEN_URL");
  await page.goto(`${site}/`);
  await expect(page.locator("h1")).toHaveText("link2579");

  // The site's cookie has gone, as it does after an hour, and a form on the
  // page posts; a POST cannot be carried across a redirect.
  await context.clearCookies({ name: "site_viewer", domain: new URL(site).hostname });
  await page.evaluate(() => {
    const f = document.createElement("form");
    f.method = "post";
    f.action = "/";
    document.body.appendChild(f);
    f.submit();
  });
  await expect(page.getByRole("heading", { name: "Sign in to open this site" })).toBeVisible();

  await page.getByRole("link", { name: "Sign in" }).click();

  await page.waitForURL(`${site}/`);
  await expect(page.locator("h1")).toHaveText("link2579");
  expect(seen, "a policy blocked the sign-in").toEqual([]);
});

test("access panel: the portal grants and revokes with the email out of the path", async ({ page }) => {
  const site = env("SITE_ACCESS_SITE");
  const email = env("SITE_ACCESS_EMAIL");
  const accessPath = `/api/v1/portal/sites/${site}/access`;

  await page.goto("/portal/?login=api_key");
  const key = page.locator('input[placeholder="X-API-Key"]');
  await key.fill(env("SITE_ACCESS_API_KEY"));
  await page.getByRole("button", { name: "Sign in with API key" }).click();
  // The key is kept for this tab only once it has been validated.
  await expect(key).toHaveCount(0);
  await page.goto(`/portal/sites/${site}/access`);

  await page.getByRole("button", { name: "Share", exact: true }).click();
  const dialog = page.getByRole("dialog");
  const box = dialog.getByRole("combobox", { name: "Name, email or persona" });
  await box.fill(email);
  await expect(dialog.getByRole("option", { name: new RegExp(email.replace(/[.+]/g, "\\$&")) })).toBeVisible();
  await box.press("Enter");
  const dev = dialog.getByRole("checkbox", { name: "dev" });
  if (!(await dev.isChecked())) await dev.check();
  const put = page.waitForRequest((r) => r.method() === "PUT" && new URL(r.url()).pathname === accessPath);
  await dialog.getByRole("button", { name: "Share" }).click();
  const sent = await put;
  expect(sent.postDataJSON()).toMatchObject({ email, level: "view", environments: expect.arrayContaining(["dev"]) });

  const row = page.getByRole("row", { name: new RegExp(email) });
  await expect(row).toBeVisible();

  const del = page.waitForRequest((r) => r.method() === "DELETE" && new URL(r.url()).pathname === accessPath);
  await row.getByRole("button", { name: "Remove" }).click();
  expect(new URL((await del).url()).searchParams.get("email")).toBe(email);
  await expect(row).toHaveCount(0);
});
