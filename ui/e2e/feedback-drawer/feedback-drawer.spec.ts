import { expect, test, type Locator, type Page } from "@playwright/test";

function env(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; test/acceptance/issue_2580_test.go and issue_2546_test.go set it`);
  return v;
}

const DESKTOP = { width: 1280, height: 800 };
const PHONE = { width: 375, height: 800 };
// The drawer is max-w-md, 448px, of which its left border takes one.
const DRAWER_WIDTH = 447;
const IDP_LOGIN = /\/realms\/[^/]+\/(protocol\/openid-connect\/auth|login-actions)/;

async function signInWithKey(page: Page): Promise<void> {
  await page.goto("/portal/?login=api_key");
  const key = page.locator('input[placeholder="X-API-Key"]');
  await key.fill(env("FEEDBACK_API_KEY"));
  await page.getByRole("button", { name: "Sign in with API key" }).click();
  // The key is kept for this tab only once it has been validated.
  await expect(key).toHaveCount(0);
}

async function signInWithPassword(page: Page): Promise<void> {
  await page.goto("/portal/auth/login");
  await page.waitForURL(IDP_LOGIN);
  await page.locator("#username").fill(env("FEEDBACK_EMAIL"));
  await page.locator("#password").fill(env("FEEDBACK_PASSWORD"));
  await page.locator("#kc-login").click();
  await page.waitForURL(/\/portal\//);
}

function threadRow(page: Page): Locator {
  return page.getByRole("button", { name: new RegExp(env("FEEDBACK_MARKER")) }).first();
}

/** openInDrawer opens the thread in the Inbox's slide-over (ThreadSlideOver). */
async function openInDrawer(page: Page): Promise<void> {
  await page.goto("/portal/feedback");
  await threadRow(page).click();
  await expect(page.getByRole("button", { name: "Back to list" })).toBeVisible();
}

/** openInPanel opens the thread in the item's own FeedbackPanel. */
async function openInPanel(page: Page): Promise<void> {
  await page.goto(`/portal/assets/${env("FEEDBACK_ASSET")}`);
  await page.getByRole("button", { name: "Feedback", exact: false }).first().click();
  await threadRow(page).click();
  await expect(page.getByRole("button", { name: "Back to list" })).toBeVisible();
}

interface Scroller {
  left: number;
  right: number;
  clientWidth: number;
  scrollWidth: number;
}

/** scroller measures the box that scrolls the thread, found from its header. */
function scroller(page: Page): Promise<Scroller> {
  return page.getByRole("button", { name: "Back to list" }).evaluate((start) => {
    let node = start.parentElement;
    while (node && !["auto", "scroll"].includes(getComputedStyle(node).overflowY)) node = node.parentElement;
    if (!node) throw new Error("the thread has no scroll container");
    const box = node.getBoundingClientRect();
    return { left: box.left, right: box.right, clientWidth: node.clientWidth, scrollWidth: node.scrollWidth };
  });
}

/** expectFits holds a control inside the scroller and clickable where it sits. */
async function expectFits(control: Locator, box: Scroller): Promise<void> {
  await expect(control).toBeVisible();
  const at = await control.boundingBox();
  expect(at, "the control has no box").not.toBeNull();
  expect(at!.x, "the control starts left of the thread").toBeGreaterThanOrEqual(box.left);
  expect(at!.x + at!.width, "the control ends past the thread's edge").toBeLessThanOrEqual(box.right);
  await control.click({ trial: true });
}

async function expectModeratorHeaderFits(page: Page, width?: number): Promise<void> {
  const box = await scroller(page);
  if (width) expect(box.clientWidth).toBe(width);
  expect(box.scrollWidth, "the thread scrolls sideways").toBeLessThanOrEqual(box.clientWidth);
  await expectFits(page.getByRole("button", { name: /^Status:/ }), box);
  await expectFits(page.getByRole("radio", { name: "Internal" }), box);
  await expectFits(page.getByRole("radio", { name: "Shared" }), box);
  await expectFits(page.getByRole("button", { name: "Delete thread" }), box);
  await expect(page.getByRole("radiogroup", { name: "Visibility" })).toBeVisible();
}

/** header is the row holding the way back, where a badge would be. */
function header(page: Page): Locator {
  return page.getByRole("button", { name: "Back to list" }).locator("xpath=../..");
}

for (const [label, viewport, width] of [["448px", DESKTOP, DRAWER_WIDTH], ["375px", PHONE, undefined]] as const) {
  test(`drawer ${label}: a moderator's header fits and shows the visibility once`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signInWithKey(page);
    await openInDrawer(page);
    await expectModeratorHeaderFits(page, width);
    await expect(header(page).getByText(/^(Internal|Shared)$/)).toHaveCount(2);
    await expect(header(page).locator("input[type=radio]")).toHaveCount(2);
    await expect(header(page).locator('[data-slot="badge"]', { hasText: /^(Internal|Shared)$/ })).toHaveCount(0);
  });

  test(`panel ${label}: the item's own feedback panel fits a moderator's header`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signInWithKey(page);
    await openInPanel(page);
    await expectModeratorHeaderFits(page, width);
  });
}

for (const [label, viewport] of [["448px", DESKTOP], ["375px", PHONE]] as const) {
  test(`badge ${label}: someone who cannot change the visibility sees the badge`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signInWithPassword(page);
    await openInDrawer(page);
    const box = await scroller(page);
    expect(box.scrollWidth, "the thread scrolls sideways").toBeLessThanOrEqual(box.clientWidth);
    await expect(header(page).getByText(env("FEEDBACK_VISIBILITY"), { exact: true })).toHaveCount(1);
    await expect(page.getByRole("radiogroup", { name: "Visibility" })).toHaveCount(0);
  });
}

test("capture shortened: a thread over the limit is captured after the reviewer shortens it", async ({ page }) => {
  await signInWithKey(page);
  await openInDrawer(page);
  const field = page.getByLabel("Insight text");
  const capture = page.getByRole("button", { name: "Capture as insight" });

  await expect(field).toHaveValue(new RegExp(env("FEEDBACK_MARKER")));
  await expect(page.getByText("4,357 of 4,000 bytes")).toBeVisible();
  await expect(page.getByText("357 bytes over the limit. Shorten the text to capture this.")).toBeVisible();
  await expect(capture).toBeDisabled();

  await field.fill(env("FEEDBACK_SHORTENED"));
  await expect(page.getByText("The insight is created from this text.")).toBeVisible();
  const posted = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/insight"));
  await capture.click();

  expect((await posted).status()).toBe(201);
  await expect(field).toHaveCount(0);
});

test("capture blocked: text over the limit cannot be submitted and the reviewer sees why", async ({ page }) => {
  const posts: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().endsWith("/insight")) posts.push(r.url());
  });
  await signInWithKey(page);
  await openInDrawer(page);
  const field = page.getByLabel("Insight text");
  const capture = page.getByRole("button", { name: "Capture as insight" });

  // 2,001 characters of two bytes each: under the limit counted in characters.
  await field.fill("é".repeat(2001));
  await expect(page.getByText("4,002 of 4,000 bytes")).toBeVisible();
  await expect(page.getByText("2 bytes over the limit. Shorten the text to capture this.")).toBeVisible();
  await expect(capture).toBeDisabled();

  await field.fill("short");
  await expect(page.getByText("Write at least 10 bytes.")).toBeVisible();
  await expect(capture).toBeDisabled();
  await field.press("Enter");

  expect(posts, "the panel submitted text it refuses").toEqual([]);
});

test("capture refused: the panel shows the server's message", async ({ page }) => {
  await signInWithKey(page);
  await openInDrawer(page);
  const posted = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/insight"));
  await page.getByRole("button", { name: "Capture as insight" }).click();

  expect((await posted).status()).toBe(400);
  await expect(page.getByRole("alert").filter({ hasText: env("FEEDBACK_REFUSAL") })).toBeVisible();
  await expect(page.getByText("Could not capture this as an insight.")).toHaveCount(0);
});
