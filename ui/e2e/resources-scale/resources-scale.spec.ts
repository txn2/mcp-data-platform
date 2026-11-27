import { test, expect, type Page } from "@playwright/test";

// #2837: the Resources page over a library of 50,000 folders and 100,000
// files stays responsive. test/acceptance/issue_2837_test.go seeds it as one
// folder of the Global library, SCALE_CASE.top, holding a folder per day, one
// day (SCALE_CASE.day) holding 5,000 message folders of two files each.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; this suite is run by test/acceptance (issue_2837)`);
  return v;
};

/** The longest one interaction may take to answer. */
const INTERACTION_MS = 1000;

async function signIn(page: Page): Promise<void> {
  await page.goto("/portal/");
  const nav = page.locator("nav");
  const apiKey = page.locator('input[placeholder="X-API-Key"]');
  const which = await Promise.race([
    nav.waitFor({ state: "visible", timeout: 60_000 }).then(() => "nav" as const),
    apiKey.waitFor({ state: "visible", timeout: 60_000 }).then(() => "login" as const),
  ]);
  if (which === "login") {
    await apiKey.fill(env("SCALE_API_KEY"));
    await page.getByRole("button", { name: /Sign in with API key/i }).click();
    await nav.waitFor({ state: "visible", timeout: 60_000 });
  }
}

/** How long fn takes to make done true. */
async function timed(page: Page, what: string, fn: () => Promise<void>, done: () => Promise<void>): Promise<number> {
  const start = Date.now();
  await fn();
  await done();
  const took = Date.now() - start;
  console.log(`${what}: ${took} ms`);
  return took;
}

test("a large library loads, opens a folder of 5,000 folders, and scrolls through them", async ({ page }) => {
  const c = JSON.parse(env("SCALE_CASE")) as { top: string; day: string };
  await signIn(page);
  const tree = page.getByTestId("folder-tree");
  const listing = page.getByTestId("listing");

  // The page opens on the folder holding every day.
  const load = await timed(
    page,
    "load the folder of 301 days",
    () => page.goto(`/portal/resources/lib/global/${c.top}`).then(() => undefined),
    () => expect(listing.getByTestId(`row-d:${c.top}/${c.day}`)).toBeVisible(),
  );
  // The load includes the portal's own scripts; the folder's answer is the part
  // this ticket is about, timed by the interactions below.
  expect(load).toBeLessThan(10_000);

  // The tree opens the folder of days (the folder in view is highlighted, its
  // ancestors open), then the day holding 5,000 folders.
  const topNode = tree.getByTestId(`tree-node-global:${c.top}`);
  await expect(topNode).toBeVisible();
  const dayNode = tree.getByTestId(`tree-node-global:${c.top}/${c.day}`);
  const days = await timed(
    page,
    "open the folder of 301 days in the tree",
    () => topNode.locator("span").first().click(),
    () => expect(dayNode).toBeVisible(),
  );
  expect(days).toBeLessThan(INTERACTION_MS);
  const open = await timed(
    page,
    "open the day of 5,000 folders in the tree",
    () => dayNode.locator("span").first().click(),
    () => expect(dayNode).toHaveAttribute("aria-expanded", "true").then(() => expect(tree.locator(`[data-node-id^="global|${c.top}/${c.day}/"]`).first()).toBeVisible()),
  );
  expect(open).toBeLessThan(INTERACTION_MS);

  // The listing shows the day's folders.
  const show = await timed(
    page,
    "show the day's 5,000 folders in the listing",
    () => dayNode.click(),
    () => expect(listing.locator(`[data-key^="d:${c.top}/${c.day}/"]`).first()).toBeVisible(),
  );
  expect(show).toBeLessThan(INTERACTION_MS);

  // And scrolls through them: each scroll to the end draws the next rows.
  const scroller = listing.locator("xpath=ancestor::*[@data-listing]");
  const rows = listing.locator(`[data-key^="d:${c.top}/${c.day}/"]`);
  let drawn = await rows.count();
  for (let i = 0; i < 5; i++) {
    const before = drawn;
    const took = await timed(
      page,
      `scroll to row ${before}`,
      () => scroller.evaluate((el) => el.scrollTo(0, el.scrollHeight)),
      () => expect.poll(() => rows.count(), { timeout: INTERACTION_MS * 5 }).toBeGreaterThan(before),
    );
    expect(took).toBeLessThan(INTERACTION_MS);
    drawn = await rows.count();
  }
  expect(drawn).toBeGreaterThan(1000);
});
