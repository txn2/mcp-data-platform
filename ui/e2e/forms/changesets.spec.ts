import { test, expect } from "@playwright/test";

// #2337: a person whose persona holds apply_knowledge, signed in through the
// identity provider, opens the Changesets tab. The tab reads routes they may
// read, and the browser stays on it: it used to read the admin API, whose
// refusal the portal took for an expired session, and the sign-in returned to
// the tab, which was refused again, forever. Run by
// test/acceptance/issue_2337_test.go.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; this case is run by test/acceptance/issue_2337_test.go`);
  return v;
};

test("criterion changesets: the Changesets tab opens for a signed-in apply_knowledge holder", async ({ page }) => {
  const signIns: string[] = [];
  page.on("framenavigated", (f) => {
    if (f === page.mainFrame() && f.url().includes("/portal/auth/login")) signIns.push(f.url());
  });
  await page.goto("/portal/auth/login?return_to=" + encodeURIComponent("/portal/knowledge#changesets"));
  await page.locator("#username").fill(env("FORMS_SIGN_IN_EMAIL"));
  await page.locator("#password").fill(env("FORMS_SIGN_IN_PASSWORD"));
  await page.locator("#kc-login").click();

  await expect(page).toHaveURL(/\/portal\/knowledge/);
  await expect(page.getByPlaceholder("Filter by Entity URN...")).toBeVisible();
  const landed = signIns.length;
  // Long enough for the tab's fetches and one refresh of the list to answer.
  await page.waitForTimeout(5000);
  expect(signIns.length, `sent to sign in again: ${signIns.join(", ")}`).toBe(landed);
  await expect(page).toHaveURL(/\/portal\/knowledge/);
  await expect(page.getByText(/session has expired/i)).toHaveCount(0);
});
