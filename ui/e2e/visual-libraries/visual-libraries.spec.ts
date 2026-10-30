import { test, expect, type Browser, type Frame, type Page } from "@playwright/test";

// #2373: each case is one skeleton from the visual-libraries page, saved by
// test/acceptance/issue_2373_test.go, which names the asset and its two shares
// in VISUALS_CASES. A case draws when one of the page's frames holds an <svg>
// or <canvas> of real size with something on it, and nothing the page's
// policy refused was reported.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; this suite is run by test/acceptance/issue_2373_test.go`);
  return v;
};

type Case = { name: string; asset: string; authShare: string; publicShare: string };

const cases = (): Case[] => JSON.parse(env("VISUALS_CASES")) as Case[];

/** Console messages and page errors reporting something the page's policy refused. */
function watchRefusals(page: Page): string[] {
  const refusals: string[] = [];
  const refused = (text: string) => /Content Security Policy|Refused to (load|connect|execute|create|compile)|WebAssembly/i.test(text);
  page.on("console", (m) => {
    if (refused(m.text())) refusals.push(m.text());
  });
  page.on("pageerror", (e) => {
    if (refused(e.message)) refusals.push(e.message);
  });
  return refusals;
}

/**
 * Whether a frame holds a drawing: an <svg> or <canvas> at least 100 by 40
 * with marks in it. An svg counts its shapes and text; a canvas counts the
 * pixels it painted. The predicate is a function, never a string: Playwright
 * evaluates a string with eval, which a share's policy refuses.
 */
async function drawnIn(frame: Frame): Promise<boolean> {
  try {
    return await frame.evaluate(() => {
      const bigEnough = (el: Element) => {
        const r = el.getBoundingClientRect();
        return r.width >= 100 && r.height >= 40;
      };
      const painted = (el: HTMLCanvasElement) => {
        const ctx = el.getContext("2d");
        if (!ctx) return true; // a WebGL canvas of real size is drawn by its library
        const d = ctx.getImageData(0, 0, el.width, el.height).data;
        let n = 0;
        for (let i = 3; i < d.length; i += 4) if (d[i] > 0) n++;
        return n > 200;
      };
      const marked = (el: Element) =>
        el.querySelectorAll("path, rect, circle, ellipse, line, polygon, polyline, text").length >= 5;
      return Array.from(document.querySelectorAll("svg, canvas")).some(
        (el) => bigEnough(el) && (el instanceof HTMLCanvasElement ? painted(el) : marked(el)),
      );
    });
  } catch {
    return false; // a frame navigating away or not yet ready
  }
}

/** Waits until some frame of the page draws; fails naming the frames and refusals. */
async function expectDrawn(page: Page, what: string, refusals: string[]) {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    for (const f of page.frames()) if (await drawnIn(f)) return;
    await page.waitForTimeout(500);
  }
  const frames = page.frames().map((f) => f.url() || "(srcdoc)");
  throw new Error(`${what}: nothing drew within 30s; frames: ${frames.join(", ")}; refused: ${refusals.join(" | ") || "nothing"}`);
}

/**
 * Signs a context in through the identity provider. An authenticated share is
 * a server page that reads the browser session cookie, which the portal's API
 * key form does not set.
 */
async function signedIn(browser: Browser, scheme: "light" | "dark"): Promise<Page> {
  const context = await browser.newContext({ colorScheme: scheme });
  const page = await context.newPage();
  await page.goto("/portal/auth/login?return_to=" + encodeURIComponent("/portal/"));
  await page.locator("#username").fill(env("VISUALS_SIGN_IN_EMAIL"));
  await page.locator("#password").fill(env("VISUALS_SIGN_IN_PASSWORD"));
  await page.locator("#kc-login").click();
  await page.locator("nav").first().waitFor({ state: "visible", timeout: 30_000 });
  return page;
}

for (const scheme of ["light", "dark"] as const) {
  for (const c of cases()) {
    test(`criterion browser: ${c.name} draws on the asset page and both shares, ${scheme}`, async ({ browser }) => {
      const signed = await signedIn(browser, scheme);
      const anon = await (await browser.newContext({ colorScheme: scheme })).newPage();
      const failures: string[] = [];
      for (const [surface, page, path] of [
        ["asset page", signed, `/portal/assets/${c.asset}`],
        ["authenticated share", signed, c.authShare],
        ["public share, signed out", anon, c.publicShare],
      ] as const) {
        const refusals = watchRefusals(page);
        await page.goto(path);
        try {
          await expectDrawn(page, `${c.name} on the ${surface} (${scheme})`, refusals);
          if (refusals.length) failures.push(`${c.name} on the ${surface} (${scheme}): refused: ${refusals.join(" | ")}`);
        } catch (e) {
          failures.push((e as Error).message);
        }
        page.removeAllListeners("console");
        page.removeAllListeners("pageerror");
      }
      expect(failures, failures.join("\n")).toEqual([]);
    });
  }
}
