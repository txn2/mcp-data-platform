import { expect, test, type Page } from "@playwright/test";

// #2705: an asset's feedback on a phone. The panel is a sheet under the item;
// with a thread open and the reply box folded to one row, the comments get
// most of the sheet, the sheet can be made full size and back, and replying
// keeps the reply and its buttons on screen. Run against the dev stack by
// test/acceptance/issue_2705_test.go, which files the thread FEEDBACK_LONG_MARKER
// names on FEEDBACK_ASSET with at least three comments of several lines each.

function env(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; test/acceptance/issue_2705_test.go sets it`);
  return v;
}

const PHONES = [
  ["390x844", { width: 390, height: 844 }],
  ["375x667", { width: 375, height: 667 }],
] as const;

async function signInWithKey(page: Page): Promise<void> {
  await page.goto("/portal/?login=api_key");
  const key = page.locator('input[placeholder="X-API-Key"]');
  await key.fill(env("FEEDBACK_API_KEY"));
  await page.getByRole("button", { name: "Sign in with API key" }).click();
  await expect(key).toHaveCount(0);
}

/** openLongThread opens the asset's feedback sheet and the long thread in it. */
async function openLongThread(page: Page): Promise<void> {
  await page.goto(`/portal/assets/${env("FEEDBACK_ASSET")}`);
  await page.getByRole("main").getByRole("button", { name: /Feedback/ }).first().click();
  await page.getByRole("button", { name: new RegExp(env("FEEDBACK_LONG_MARKER")) }).first().click();
  await expect(page.getByRole("button", { name: "Back to list" })).toBeVisible();
}

interface Measure {
  sheet: number;
  timeline: number;
  scrollers: number[];
}

/**
 * measure is the sheet's height, how much of it shows the timeline, and the
 * height of every box inside the sheet that scrolls on its own.
 */
function measure(page: Page): Promise<Measure> {
  return page.locator("[data-item-panel]").evaluate((sheet) => {
    const box = sheet.getBoundingClientRect();
    const scroll = sheet.querySelector('[data-testid="thread-scroll"]')!.getBoundingClientRect();
    const line = sheet.querySelector('[data-testid="thread-timeline"]')!.getBoundingClientRect();
    const timeline = Math.max(0, Math.min(scroll.bottom, line.bottom) - Math.max(scroll.top, line.top));
    const scrollers = [...sheet.querySelectorAll<HTMLElement>("*")]
      .filter((el) => ["auto", "scroll"].includes(getComputedStyle(el).overflowY) && el.scrollHeight > el.clientHeight)
      .map((el) => el.clientHeight);
    return { sheet: box.height, timeline, scrollers };
  });
}

for (const [label, viewport] of PHONES) {
  test(`sheet ${label}: the comments get most of the sheet with the reply box folded`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signInWithKey(page);
    await openLongThread(page);
    await expect(page.getByRole("button", { name: "Reply…" })).toBeVisible();
    await expect(page.getByRole("toolbar", { name: "Formatting" })).toHaveCount(0);

    const m = await measure(page);
    expect(m.timeline, `the timeline shows ${m.timeline}px of a ${m.sheet}px sheet`).toBeGreaterThanOrEqual(m.sheet * 0.5);
    for (const h of m.scrollers) {
      expect(h, "a scroll strip inside the sheet is under a third of it").toBeGreaterThanOrEqual(m.sheet / 3);
    }
  });

  test(`sheet ${label}: made full size and back, the size kept between threads`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signInWithKey(page);
    await openLongThread(page);
    const before = (await measure(page)).sheet;
    await page.getByTestId("sheet-handle").click();
    await expect(page.locator("[data-item-panel]")).toHaveAttribute("data-sheet-size", "full");
    const full = (await measure(page)).sheet;
    expect(full).toBeGreaterThan(before);
    await page.getByRole("button", { name: "Back to list" }).click();
    await page.getByRole("button", { name: new RegExp(env("FEEDBACK_LONG_MARKER")) }).first().click();
    await expect(page.locator("[data-item-panel]")).toHaveAttribute("data-sheet-size", "full");
    await page.getByRole("button", { name: "Make the feedback sheet smaller" }).first().click();
    await expect(page.locator("[data-item-panel]")).toHaveAttribute("data-sheet-size", "default");
  });

  test(`sheet ${label}: replying keeps the reply, its buttons and the last comment on screen`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signInWithKey(page);
    await openLongThread(page);
    await page.getByRole("button", { name: "Reply…" }).click();
    const reply = page.getByLabel("Reply", { exact: true });
    await reply.fill("Week 45 too, please.");
    await expect(reply).toBeInViewport();
    await expect(page.getByRole("button", { name: "Reply", exact: true })).toBeInViewport();
    await expect(page.getByRole("button", { name: "Expand the reply box" })).toHaveCount(0);
    const last = page.getByTestId("thread-timeline").locator(":scope > *").last();
    await expect(last).toBeInViewport({ ratio: 0.1 });
  });
}

test("sheet 1280: beside the page the panel is a full-height column with the reply box open", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await signInWithKey(page);
  await openLongThread(page);
  await expect(page.getByTestId("sheet-handle")).toHaveCount(0);
  await expect(page.getByLabel("Reply", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Expand the reply box" })).toBeVisible();
  const panel = (await page.locator("[data-item-panel]").boundingBox())!;
  expect(panel.width).toBeLessThan(500);
});
