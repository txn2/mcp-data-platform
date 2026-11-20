import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// #2727: a page left open across an upgrade moves to the new build. The served
// index.html names the build the page loads (a meta element) and every answer
// names the build that served it (X-Platform-Build). The mock server has
// neither, so an init script plays the server: before the app's own scripts it
// writes the meta element and stamps every answer with the build the test
// sets, both read from sessionStorage, so a reload "fetches" the build the
// test switched to, as a reload fetches the current index.html.

const NOTICE = "The portal has been updated. Save your changes, then reload to use the new version.";

async function serveBuilds(page: Page, initial: string): Promise<void> {
  await page.addInitScript((first) => {
    const build = () => sessionStorage.getItem("e2e-served-build") ?? first;
    if (!sessionStorage.getItem("e2e-served-build")) sessionStorage.setItem("e2e-served-build", first);
    // The document has no element yet when this runs; the meta goes into the
    // head as soon as the parser makes one, before the app's module runs.
    const addMeta = (): boolean => {
      if (!document.head) return false;
      const meta = document.createElement("meta");
      meta.name = "mcp-platform-build";
      meta.content = build();
      document.head.appendChild(meta);
      return true;
    };
    if (!addMeta()) {
      const watch = new MutationObserver(() => {
        if (addMeta()) watch.disconnect();
      });
      watch.observe(document, { childList: true, subtree: true });
    }
    const original = window.fetch.bind(window);
    window.fetch = async (...args: Parameters<typeof fetch>) => {
      const res = await original(...args);
      const headers = new Headers(res.headers);
      headers.set("X-Platform-Build", build());
      return new Response(res.body, { status: res.status, statusText: res.statusText, headers });
    };
  }, initial);
}

/** upgrade switches the build the "server" answers with, and has the page hear of it. */
async function upgrade(page: Page, build: string): Promise<void> {
  await page.evaluate(async (b) => {
    sessionStorage.setItem("e2e-served-build", b);
    await fetch("/api/v1/portal/me");
  }, build);
}

async function pageBuild(page: Page): Promise<string | null> {
  return page.locator('meta[name="mcp-platform-build"]').getAttribute("content");
}

test.describe("An open portal moves to a new build (#2727)", () => {
  test("a page on build A that hears of build B reloads at the next navigation and runs B", async ({ page }) => {
    await serveBuilds(page, "v1+a");
    await authenticate(page);
    expect(await pageBuild(page)).toBe("v1+a");
    await page.evaluate(() => ((window as unknown as { e2eSameDocument: boolean }).e2eSameDocument = true));

    await upgrade(page, "v2+b");
    await expect(page.getByText(NOTICE)).toHaveCount(0);
    await page.locator("nav").getByRole("button", { name: "Sites", exact: true }).first().click();

    await expect(page).toHaveURL(/\/portal\/sites/);
    await expect.poll(() => page.evaluate(() => (window as unknown as { e2eSameDocument?: boolean }).e2eSameDocument ?? false)).toBe(false);
    expect(await pageBuild(page)).toBe("v2+b");

    // Running B, an answer naming B moves nothing: the next navigation stays
    // in the page.
    await page.evaluate(() => ((window as unknown as { e2eSameDocument: boolean }).e2eSameDocument = true));
    await page.evaluate(() => fetch("/api/v1/portal/me"));
    await page.locator("nav").getByRole("button", { name: "Home", exact: true }).first().click();
    await expect(page).toHaveURL(/\/portal\/?$/);
    expect(await page.evaluate(() => (window as unknown as { e2eSameDocument?: boolean }).e2eSameDocument)).toBe(true);
  });

  test("with a dialog open the page shows the notice instead of reloading, and Reload works", async ({ page }) => {
    await serveBuilds(page, "v1+a");
    await authenticate(page);
    await page.goto("/portal/sites");
    await page.getByRole("button", { name: "New site" }).click();
    await expect(page.getByRole("dialog")).toBeVisible();

    await upgrade(page, "v2+b");
    // The open dialog makes the rest of the page inert; the notice is drawn
    // behind it, and the page did not reload.
    await expect(page.getByText(NOTICE)).toHaveCount(1);
    expect(await pageBuild(page)).toBe("v1+a");

    // Closing the dialog leaves the notice, and Reload moves the page.
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0);
    const notice = page.getByRole("status").filter({ hasText: NOTICE });
    await expect(notice).toBeVisible();
    await notice.getByRole("button", { name: "Reload" }).click();
    await expect.poll(() => pageBuild(page)).toBe("v2+b");
    await expect(page.getByText(NOTICE)).toHaveCount(0);
  });

  test("an answer from a replica on a build the page moved past causes no reload", async ({ page }) => {
    await serveBuilds(page, "v1+a");
    await authenticate(page);
    await upgrade(page, "v2+b");
    await page.locator("nav").getByRole("button", { name: "Sites", exact: true }).first().click();
    await expect.poll(() => pageBuild(page)).toBe("v2+b");

    // A replica still on A answers: A is a build this tab already ran.
    await page.evaluate(() => ((window as unknown as { e2eSameDocument: boolean }).e2eSameDocument = true));
    await upgrade(page, "v1+a");
    await page.locator("nav").getByRole("button", { name: "Home", exact: true }).first().click();
    await expect(page).toHaveURL(/\/portal\/?$/);
    expect(await page.evaluate(() => (window as unknown as { e2eSameDocument?: boolean }).e2eSameDocument)).toBe(true);
    await expect(page.getByText(NOTICE)).toHaveCount(0);
  });
});
