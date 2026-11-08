import fs from "fs";
import path from "path";
import { test, expect, type Browser, type Page } from "@playwright/test";

// #2530: Download of an HTML or JSX asset is one file that opens from disk
// with the network off and renders as it does in the portal; an asset that
// reads the platform's data offers its stored source instead; every other type
// downloads as before. Each case is one asset test/acceptance/issue_2530_test.go
// saved, named in SA_CASES with the surfaces to download it from and what to
// check in the file. Downloads are kept under SA_OUT.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; this suite is run by test/acceptance (issue_2530)`);
  return v;
};

type Surface = "asset" | "auth" | "public";
type Check = "deck" | "jsx-chart" | "font" | "image" | "static-map" | "published" | "dialog" | "plain";
type Case = {
  check: Check;
  label: string;
  name: string;
  asset: string;
  authShare?: string;
  publicShare?: string;
  surfaces: Surface[];
  slides?: number;
  font?: string;
  storedB64?: string;
  contentB64?: string;
  screenshots?: boolean;
};

const CASES: Case[] = JSON.parse(env("SA_CASES")) as Case[];
const OUT = env("SA_OUT");
const API_KEY = env("SA_API_KEY");

fs.mkdirSync(OUT, { recursive: true });

/**
 * Signs the context in through the dev stack's Keycloak, the browser session
 * a reader has: a session cookie, which no downloaded file may carry, and the
 * only sign-in an authenticated share admits.
 */
async function signIn(page: Page): Promise<void> {
  await page.goto("/portal/auth/login?return_to=" + encodeURIComponent("/portal/"));
  await page.locator("#username").fill(env("SA_SIGN_IN_EMAIL"));
  await page.locator("#password").fill(env("SA_SIGN_IN_PASSWORD"));
  await page.locator("#kc-login").click();
  await page.locator("nav").first().waitFor({ state: "visible", timeout: 30_000 });
}

/** A page on the surface, signed in for the asset page and an authenticated share. */
async function open(browser: Browser, c: Case, surface: Surface, dark = false): Promise<Page> {
  const context = await browser.newContext({ acceptDownloads: true, colorScheme: dark ? "dark" : "light" });
  if (surface !== "public") {
    const signInPage = await context.newPage();
    await signIn(signInPage);
    await signInPage.close();
  }
  const page = await context.newPage();
  const target = surface === "asset" ? `/portal/assets/${c.asset}` : surface === "auth" ? c.authShare! : c.publicShare!;
  await page.goto(target);
  return page;
}

/**
 * Clicks Download where the surface offers it. A signed-in reader opening an
 * authenticated share link is shown the asset in the portal, so it is the asset
 * page's Download there too; a public share's is in the share page's toolbar.
 */
async function clickDownload(page: Page, surface: Surface): Promise<void> {
  if (surface === "asset" || surface === "auth") {
    await page.getByRole("button", { name: "Download", exact: true }).first().click();
  } else {
    await page.locator("#content-actions").getByRole("button", { name: "Download" }).click();
  }
}

/** Downloads the case from the surface and returns the saved file's path. */
async function download(page: Page, c: Case, surface: Surface): Promise<string> {
  const wait = page.waitForEvent("download");
  await clickDownload(page, surface);
  const dl = await wait;
  const file = path.join(OUT, `${c.label}-${surface}-${dl.suggestedFilename()}`);
  await dl.saveAs(file);
  return file;
}

/** No downloaded file carries the reader's credentials. */
function assertNoCredentials(file: string, cookies: string[]): void {
  const body = fs.readFileSync(file).toString("latin1");
  for (const secret of [API_KEY, ...cookies].filter((s) => s.length >= 8)) {
    expect(body.includes(secret), `${file} carries a credential as written`).toBe(false);
    expect(body.includes(Buffer.from(secret).toString("base64")), `${file} carries a credential as base64`).toBe(false);
  }
}

/** Opens a downloaded file from disk with the network off, failing on any request or error. */
async function openOffline(browser: Browser, file: string): Promise<{ page: Page; requests: string[]; errors: string[] }> {
  const context = await browser.newContext({ offline: true });
  const page = await context.newPage();
  const requests: string[] = [];
  const errors: string[] = [];
  page.on("request", (r) => {
    if (!/^(file|data|blob|about):/.test(r.url())) requests.push(r.url());
  });
  page.on("requestfailed", (r) => {
    if (!/^(data|blob):/.test(r.url())) errors.push(`request failed: ${r.url()}`);
  });
  page.on("pageerror", (e) => errors.push(`page error: ${e.message}`));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(`console: ${m.text()}`);
  });
  await page.goto("file://" + file);
  return { page, requests, errors };
}

async function checkOffline(browser: Browser, c: Case, file: string): Promise<void> {
  const { page, requests, errors } = await openOffline(browser, file);
  switch (c.check) {
    case "deck": {
      await page.waitForFunction(() => {
        const r = (window as unknown as { Reveal?: { isReady?: () => boolean } }).Reveal;
        return !!r && typeof r.isReady === "function" && r.isReady();
      });
      expect(await page.evaluate(() => (window as unknown as { Reveal: { getTotalSlides: () => number } }).Reveal.getTotalSlides())).toBe(c.slides);
      const bg = await page.evaluate(() => getComputedStyle(document.querySelector(".reveal-viewport") ?? document.body).backgroundColor);
      expect(bg, "the black theme is applied").toBe("rgb(25, 25, 25)");
      const before = await page.evaluate(() => (window as unknown as { Reveal: { getIndices: () => { h: number } } }).Reveal.getIndices().h);
      await page.keyboard.press("ArrowRight");
      await expect
        .poll(() => page.evaluate(() => (window as unknown as { Reveal: { getIndices: () => { h: number } } }).Reveal.getIndices().h))
        .toBe(before + 1);
      break;
    }
    case "jsx-chart":
      await expect(page.locator("svg.recharts-surface")).toBeVisible();
      await expect(page.locator("svg.lucide")).toBeVisible();
      await expect.poll(() => page.locator("#logo").evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0)).toBe(true);
      break;
    case "font":
      await page.evaluate(() => document.fonts.ready);
      await expect.poll(() => page.evaluate((f) => document.fonts.check(`20px "${f}"`), c.font)).toBe(true);
      expect(await page.evaluate((f) => [...document.fonts].some((ff) => ff.family.replace(/"/g, "") === f && ff.status === "loaded"), c.font)).toBe(true);
      break;
    case "image":
      await expect.poll(() => page.locator("#pic").evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0)).toBe(true);
      break;
    case "static-map":
      await expect.poll(() => page.evaluate(() => (window as unknown as { __mapLoaded?: boolean }).__mapLoaded === true)).toBe(true);
      await expect(page.locator("canvas.maplibregl-canvas")).toBeVisible();
      break;
    case "published":
      await expect(page.locator("#total")).toHaveText("42");
      break;
    default:
      throw new Error(`no offline check for ${c.check}`);
  }
  expect(requests, "the file made no request").toEqual([]);
  expect(errors, "the console and network log show no failure").toEqual([]);
  await page.context().close();
}

for (const c of CASES) {
  for (const surface of c.surfaces) {
    test(`${c.label} from the ${surface} page`, async ({ browser }) => {
      const page = await open(browser, c, surface);
      const cookies = (await page.context().cookies()).map((k) => k.value);
      if (c.check === "dialog") {
        await clickDownload(page, surface);
        const dialog = page.getByRole("dialog");
        await expect(dialog.getByText("This asset reads data from the platform")).toBeVisible();
        if (c.screenshots && surface === "asset") {
          // The dialog fades in; the picture is taken once it is fully drawn.
          await expect(dialog).toHaveCSS("opacity", "1");
          await page.screenshot({ path: path.join(OUT, "dialog-light.png") });
          const darkPage = await open(browser, c, surface, true);
          await clickDownload(darkPage, surface);
          const darkDialog = darkPage.getByRole("dialog");
          await expect(darkDialog.getByText("This asset reads data from the platform")).toBeVisible();
          await expect(darkDialog).toHaveCSS("opacity", "1");
          await darkPage.screenshot({ path: path.join(OUT, "dialog-dark.png") });
          await darkPage.context().close();
        }
        let downloaded = false;
        page.on("download", () => (downloaded = true));
        await dialog.getByRole("button", { name: "Cancel" }).click();
        await expect(dialog).toBeHidden();
        await page.waitForTimeout(1500);
        expect(downloaded, "Cancel downloads nothing").toBe(false);

        await clickDownload(page, surface);
        const wait = page.waitForEvent("download");
        await page.getByRole("dialog").getByRole("button", { name: "Download source" }).click();
        const dl = await wait;
        const file = path.join(OUT, `${c.label}-${surface}-source-${dl.suggestedFilename()}`);
        await dl.saveAs(file);
        expect(fs.readFileSync(file).toString("base64"), "the source is saved as stored, byte for byte").toBe(c.storedB64);
        assertNoCredentials(file, cookies);
      } else if (c.check === "plain") {
        const file = await download(page, c, surface);
        expect(path.basename(file).endsWith(c.name), "the file keeps the asset's name").toBe(true);
        expect(fs.readFileSync(file).toString("base64"), "the content route's bytes, as before").toBe(c.contentB64);
      } else {
        const file = await download(page, c, surface);
        expect(file.endsWith(".html")).toBe(true);
        console.log(`standalone size ${c.label} ${surface}: ${fs.statSync(file).size} bytes`);
        assertNoCredentials(file, cookies);
        await checkOffline(browser, c, file);
      }
      await page.context().close();
    });
  }
}
