import { test, expect, type Locator, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// #2614: Download shows a download running from the click until the file is
// handed to the browser, says why one failed, and starts no second one while
// the first runs. The page's fetch is wrapped before the app loads, and once a
// test turns it on it answers the download's own request: in pieces the test
// releases one at a time (a body of a known or an unknown size), with its
// headers held back first (a standalone file being built), or 500. The pages
// load against MSW as usual before it is turned on.

type Mode = "off" | "pieces" | "prepare" | "fail";

async function wrapFetch(page: Page): Promise<void> {
  await page.addInitScript(() => {
    type Dl = { mode: string; total: number; pieces: number; withLength: boolean; requests: string[]; next: () => void };
    const w = window as unknown as { __dl: Dl };
    const waiting: (() => void)[] = [];
    let credit = 0;
    const gate = () =>
      new Promise<void>((resolve) => {
        if (credit > 0) {
          credit -= 1;
          resolve();
        } else waiting.push(resolve);
      });
    w.__dl = {
      mode: "off",
      total: 4 * 1024 * 1024,
      pieces: 4,
      withLength: true,
      requests: [],
      next() {
        const release = waiting.shift();
        if (release) release();
        else credit += 1;
      },
    };
    const native = window.fetch.bind(window);
    window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const d = w.__dl;
      if (d.mode === "off" || !/\/(content|standalone)(\?|$)/.test(url)) return native(input, init);
      d.requests.push(url);
      if (d.mode === "fail") return Promise.resolve(new Response("boom", { status: 500 }));
      let sent = 0;
      const piece = new Uint8Array(d.total / d.pieces);
      const body = new ReadableStream<Uint8Array>({
        async pull(c) {
          if (sent === d.pieces) {
            c.close();
            return;
          }
          await gate();
          sent += 1;
          c.enqueue(piece);
        },
      });
      const headers: Record<string, string> = { "Content-Type": "application/octet-stream" };
      if (d.withLength) headers["Content-Length"] = String(d.total);
      const answer = () => new Response(body, { status: 200, headers });
      return d.mode === "prepare" ? gate().then(answer) : Promise.resolve(answer());
    };
  });
}

async function setMode(page: Page, mode: Mode, withLength = true): Promise<void> {
  await page.evaluate(([m, l]) => Object.assign((window as unknown as { __dl: object }).__dl, { mode: m, withLength: l }), [mode, withLength] as const);
}
const next = (page: Page) => page.evaluate(() => (window as unknown as { __dl: { next: () => void } }).__dl.next());
const requests = (page: Page) => page.evaluate(() => (window as unknown as { __dl: { requests: string[] } }).__dl.requests.length);

/** Clicks a busy control: aria-disabled, so Playwright's own click would wait for it. */
const clickAnyway = (l: Locator) => l.dispatchEvent("click");

/**
 * Runs a gated four-piece download through control: disabled with progress
 * within 500 ms of the click, rising piece by piece, a second click starting
 * nothing, and the file saved and the control back to Download at the end.
 */
async function downloadsWithProgress(page: Page, control: Locator, start: () => Promise<void>): Promise<void> {
  const saved = page.waitForEvent("download");
  const clicked = Date.now();
  await start();
  await expect(control).toBeDisabled({ timeout: 500 });
  expect(Date.now() - clicked).toBeLessThan(1500);
  await expect(control).toHaveText("Downloading 0%");
  await clickAnyway(control);
  for (const share of [25, 50, 75]) {
    await next(page);
    await expect(control).toHaveText(`Downloading ${share}%`);
  }
  await expect(control).toHaveAttribute("title", "3 MB of 4 MB");
  expect(await requests(page)).toBe(1);
  await next(page);
  await saved;
  await expect(control).toHaveText("Download");
  await expect(control).toBeEnabled();
  expect(await requests(page)).toBe(1);
}

test.describe("Download progress (#2614)", () => {
  test.beforeEach(async ({ page }) => {
    await wrapFetch(page);
    await authenticate(page);
  });

  test("an asset's Download shows the download running and saves the file", async ({ page }) => {
    await page.goto("/portal/assets/ast-003");
    const control = page.getByTestId("download").first();
    await expect(control).toHaveText("Download", { timeout: 20_000 });
    await setMode(page, "pieces");
    await downloadsWithProgress(page, control, () => control.click());
  });

  test("a size the platform does not declare is counted in bytes", async ({ page }) => {
    await page.goto("/portal/assets/ast-003");
    const control = page.getByTestId("download").first();
    await expect(control).toHaveText("Download", { timeout: 20_000 });
    await setMode(page, "pieces", false);
    const saved = page.waitForEvent("download");
    await control.click();
    await expect(control).toHaveText("Downloading…");
    await next(page);
    await expect(control).toHaveText("Downloading 1 MB");
    await expect(control).toHaveAttribute("title", "1 MB so far");
    for (let i = 0; i < 3; i++) await next(page);
    await saved;
    await expect(control).toHaveText("Download");
  });

  test("an HTML asset says Preparing… until the platform answers", async ({ page }) => {
    await page.goto("/portal/assets/ast-001");
    const control = page.getByTestId("download").first();
    await expect(control).toHaveText("Download", { timeout: 20_000 });
    await setMode(page, "prepare");
    const saved = page.waitForEvent("download");
    await control.click();
    await expect(control).toHaveText("Preparing…");
    await expect(control).toHaveAttribute("title", "Making one file that opens offline");
    await next(page);
    await expect(control).toHaveText("Downloading 0%");
    for (let i = 0; i < 4; i++) await next(page);
    expect((await saved).suggestedFilename()).toMatch(/\.html$/);
    await expect(control).toHaveText("Download");
  });

  test("a refused download names the status and leaves Download to try again", async ({ page }) => {
    await page.goto("/portal/assets/ast-003");
    const control = page.getByTestId("download").first();
    await expect(control).toHaveText("Download", { timeout: 20_000 });
    await setMode(page, "fail");
    await control.click();
    const toast = page.getByTestId("download-failure");
    await expect(toast).toContainText("Download failed: the platform answered 500.");
    await expect(control).toBeEnabled();
    await setMode(page, "pieces");
    await toast.getByRole("button", { name: "Try again" }).click();
    await expect(control).toHaveText("Downloading 0%");
    for (let i = 0; i < 4; i++) await next(page);
    await expect(control).toHaveText("Download");
  });

  test("a managed resource's Download on its page", async ({ page }) => {
    await page.goto("/portal/resources/res-001");
    const control = page.getByTestId("resource-detail-actions").getByTestId("download");
    await expect(control).toHaveText("Download", { timeout: 20_000 });
    await setMode(page, "pieces");
    await downloadsWithProgress(page, control, () => control.click());
    await setMode(page, "fail");
    await control.click();
    await expect(page.getByTestId("download-failure")).toContainText("Download failed: the platform answered 500.");
    await expect(control).toBeEnabled();
  });

  test("a managed resource's Download in the file browser", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/portal/resources/lib/global/documentation");
    await page.getByTestId("row-res-001").click();
    const control = page.getByTestId("preview-file").getByTestId("download");
    await expect(control).toHaveText("Download", { timeout: 20_000 });
    await setMode(page, "pieces");
    await downloadsWithProgress(page, control, () => control.click());
  });

  test("on a phone the More menu's Download shows a status line until the download ends", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/portal/assets/ast-003");
    const more = page.getByTestId("phone-toolbar").getByRole("button", { name: "More" });
    await expect(more).toBeVisible({ timeout: 20_000 });
    await setMode(page, "pieces");
    const saved = page.waitForEvent("download");
    await more.click();
    await page.getByRole("menuitem", { name: "Download" }).click();
    const status = page.getByTestId("download-status");
    await expect(status).toContainText("Downloading Weekly Inventory Report");
    await expect(status).toContainText("0%");
    await next(page);
    await expect(status).toContainText("25%");
    await expect(status).toContainText("1 MB of 4 MB");
    const box = await status.boundingBox();
    expect(box && box.x >= 0 && box.x + box.width <= 390).toBe(true);
    await more.click();
    await expect(page.getByRole("menuitem", { name: "Download" })).toBeDisabled();
    await page.keyboard.press("Escape");
    for (let i = 0; i < 3; i++) await next(page);
    await saved;
    await expect(status).toHaveCount(0);
    expect(await requests(page)).toBe(1);
  });

  test("on a phone a failure stays until it is dismissed", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/portal/assets/ast-003");
    const more = page.getByTestId("phone-toolbar").getByRole("button", { name: "More" });
    await expect(more).toBeVisible({ timeout: 20_000 });
    await setMode(page, "fail");
    await more.click();
    await page.getByRole("menuitem", { name: "Download" }).click();
    const status = page.getByRole("alert").filter({ hasText: "Download failed" });
    await expect(status).toContainText("Download failed: the platform answered 500.");
    await expect(status.getByRole("button", { name: "Try again" })).toBeVisible();
    await status.getByRole("button", { name: "Dismiss" }).click();
    await expect(status).toHaveCount(0);
  });
});
