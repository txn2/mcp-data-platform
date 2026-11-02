import { test, expect, type Browser, type FrameLocator, type Page } from "@playwright/test";

// #2069 and #2395: each case is one HTML asset test/acceptance saved, named in
// EMBED_CASES with its public share and what to check of its video.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; this suite is run by test/acceptance (issue_2069, issue_2395)`);
  return v;
};

type Case = {
  name: string;
  asset: string;
  publicShare?: string;
  /** A <video> in the document: whether it plays, and whether it seeks. */
  videos?: { selector: string; plays: boolean; seeks?: boolean }[];
  /** A player frame by host: whether the frame's policy lets it load. */
  frames?: { host: string; allowed: boolean }[];
  /** A YouTube or Vimeo card, and the address its button opens. */
  cards?: { video: string; opens: string }[];
};

const cases = (): Case[] => JSON.parse(env("EMBED_CASES")) as Case[];

/** Console messages reporting something a policy refused, which name the address. */
function watchRefusals(page: Page): string[] {
  const refusals: string[] = [];
  const refused = (text: string) => /Content Security Policy|Refused to (load|frame|connect)/i.test(text);
  page.on("console", (m) => {
    if (refused(m.text())) refusals.push(m.text());
  });
  return refusals;
}

async function signedIn(browser: Browser): Promise<Page> {
  const page = await (await browser.newContext()).newPage();
  await page.goto("/portal/auth/login?return_to=" + encodeURIComponent("/portal/"));
  await page.locator("#username").fill(env("EMBED_SIGN_IN_EMAIL"));
  await page.locator("#password").fill(env("EMBED_SIGN_IN_PASSWORD"));
  await page.locator("#kc-login").click();
  await page.locator("nav").first().waitFor({ state: "visible", timeout: 30_000 });
  return page;
}

/** The asset's own frame: the srcdoc document the viewer draws an HTML asset in. */
function assetFrame(page: Page): FrameLocator {
  return page.frameLocator('iframe[title="HTML Preview"]');
}

async function checkVideo(page: Page, frame: FrameLocator, v: NonNullable<Case["videos"]>[number], refusals: string[]) {
  const video = frame.locator(v.selector);
  await expect(video, `${v.selector} is not in the document`).toHaveCount(1);
  if (!v.plays) {
    await expect
      .poll(async () => refusals.some((r) => /media/i.test(r)) || (await video.evaluate((el: HTMLVideoElement) => el.error !== null || el.networkState === 3)), {
        message: `${v.selector} was not refused`,
      })
      .toBe(true);
    expect(await video.evaluate((el: HTMLVideoElement) => el.readyState), `${v.selector} loaded`).toBe(0);
    return;
  }
  await video.evaluate((el: HTMLVideoElement) => {
    el.muted = true;
    return el.play();
  });
  await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.currentTime), { message: `${v.selector} does not play` }).toBeGreaterThan(0.5);
  if (v.seeks) {
    await video.evaluate(
      (el: HTMLVideoElement) =>
        new Promise<void>((resolve) => {
          el.pause();
          el.addEventListener("seeked", () => resolve(), { once: true });
          el.currentTime = 12;
        }),
    );
    expect(await video.evaluate((el: HTMLVideoElement) => Math.round(el.currentTime))).toBe(12);
    await expect.poll(() => video.evaluate((el: HTMLVideoElement) => el.readyState), { message: `${v.selector} did not load after seeking` }).toBeGreaterThanOrEqual(2);
  }
}

async function checkFrame(page: Page, f: NonNullable<Case["frames"]>[number], refusals: string[]) {
  if (f.allowed) {
    await expect
      .poll(() => page.frames().some((fr) => fr.url().includes(f.host)), { message: `the ${f.host} frame did not load` })
      .toBe(true);
    expect(refusals.filter((r) => r.includes(f.host)), `the ${f.host} frame was refused`).toEqual([]);
    return;
  }
  await expect.poll(() => refusals.some((r) => r.includes(f.host)), { message: `the ${f.host} frame was not refused` }).toBe(true);
}

async function checkCard(page: Page, frame: FrameLocator, card: NonNullable<Case["cards"]>[number]) {
  const button = frame.locator(`[data-mcp-video="${card.video}"]`);
  await expect(button, `no card for ${card.video}`).toBeVisible();
  const opened = page.context().waitForEvent("request", { predicate: (r) => r.url().startsWith(card.opens), timeout: 30_000 });
  await button.click();
  await opened;
}

for (const c of cases()) {
  test(`criterion browser: ${c.name}`, async ({ browser }) => {
    const signed = await signedIn(browser);
    const anon = await (await browser.newContext()).newPage();
    const surfaces: Array<[string, Page, string]> = [["asset page", signed, `/portal/assets/${c.asset}`]];
    if (c.publicShare) surfaces.push(["public share, signed out", anon, c.publicShare]);
    const failures: string[] = [];
    for (const [surface, page, path] of surfaces) {
      const refusals = watchRefusals(page);
      await page.goto(path);
      const frame = assetFrame(page);
      try {
        await expect(page.locator('iframe[title="HTML Preview"]')).toHaveAttribute("allow", /fullscreen/);
        for (const v of c.videos ?? []) await checkVideo(page, frame, v, refusals);
        for (const f of c.frames ?? []) await checkFrame(page, f, refusals);
        for (const card of c.cards ?? []) await checkCard(page, frame, card);
      } catch (e) {
        failures.push(`${c.name} on the ${surface}: ${(e as Error).message}`);
      }
      page.removeAllListeners("console");
    }
    expect(failures, failures.join("\n")).toEqual([]);
  });
}
