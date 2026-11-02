import { test, expect, type Browser, type Page } from "@playwright/test";

// #2303, #2296, #2302, #2306 and #2308: each case is one asset
// test/acceptance saved, named in DRAWN_CASES with the shares it made and what
// to check of its drawing.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; this suite is run by test/acceptance (issue_2303, issue_2296)`);
  return v;
};

type Case = {
  name: string;
  kind: "graphviz" | "vegalite" | "d2" | "excalidraw" | "lottie";
  asset: string;
  authShare?: string;
  publicShare?: string;
  /** The share page's source toggle label; the asset page always says "Source". */
  sourceLabel: string;
  /** A string the source view shows. */
  sourceText: string;
  /** No <a> and no javascript: anywhere in the drawing. */
  noLinks?: boolean;
  /** Graphviz: whether the root's three children (left, middle, right) share a row (dot) or not (neato). */
  leavesShareARow?: boolean;
  /** How many marks the drawing holds, by selector inside it. */
  marks?: { selector: string; count: number };
  /** Vega-Lite: clicking the first mark of the first view filters the second view's marks. */
  selection?: { pick: string; filtered: string };
  /** Excalidraw: the asset page's owner gets the editor, every share a read-only scene. */
  editorForOwnerOnly?: boolean;
  /** Excalidraw: a share path a signed-in reader without edit access opens, read-only. */
  viewerShare?: string;
  /** Excalidraw: draw a rectangle on the asset page and save it as a new version. */
  drawAndSave?: boolean;
  /** Lottie: pause, then scrub to a frame, on the asset page. */
  playback?: boolean;
  /** The page the drawing opens on, when it is not an asset's (an uploaded file's resource page). */
  page?: string;
};

/** Where each kind's drawing is placed. */
const DRAWINGS: Record<Case["kind"], string> = {
  graphviz: '[data-testid="graphviz-drawing"] svg',
  vegalite: '[data-testid="vegalite-drawing"] svg',
  d2: '[data-testid="d2-drawing"] svg',
  excalidraw: '[data-testid="excalidraw-scene"] canvas',
  lottie: '[data-testid="lottie-player"] svg',
};

const cases = (): Case[] => JSON.parse(env("DRAWN_CASES")) as Case[];

const drawingSelector = (c: Case) => DRAWINGS[c.kind];

/** Console messages and page errors reporting something the page's policy refused. */
function watchRefusals(page: Page): string[] {
  const refusals: string[] = [];
  const refused = (text: string) => /Content Security Policy|Refused to (load|connect|execute|create|compile)|unsafe-eval/i.test(text);
  page.on("console", (m) => {
    if (refused(m.text())) refusals.push(m.text());
  });
  page.on("pageerror", (e) => {
    if (refused(e.message)) refusals.push(e.message);
  });
  return refusals;
}

/** Waits for the drawing: an svg in the case's drawing host with its marks, or a scene's canvas. */
async function expectDrawn(page: Page, c: Case, what: string) {
  const svg = page.locator(drawingSelector(c)).first();
  await expect(svg, `${what}: no drawing`).toBeVisible();
  if (c.kind === "excalidraw") return;
  await expect
    .poll(
      () => svg.evaluate((el) => el.querySelectorAll("path, rect, ellipse, polygon, text, line").length),
      { message: `${what}: the drawing holds no marks` },
    )
    .toBeGreaterThanOrEqual(c.kind === "lottie" ? 1 : 5);
}

/**
 * Signs a context in through the identity provider: an authenticated share
 * reads the session cookie that sign-in sets.
 */
async function signedIn(browser: Browser, as: "owner" | "reader" = "owner"): Promise<Page> {
  const page = await (await browser.newContext()).newPage();
  await page.goto("/portal/auth/login?return_to=" + encodeURIComponent("/portal/"));
  const who = as === "owner" ? "DRAWN_SIGN_IN" : "DRAWN_READER";
  await page.locator("#username").fill(env(`${who}_EMAIL`));
  await page.locator("#password").fill(env(`${who}_PASSWORD`));
  await page.locator("#kc-login").click();
  await page.locator("nav").first().waitFor({ state: "visible", timeout: 30_000 });
  return page;
}

/** The asset page's Source view shows the source, and Preview brings the drawing back. */
async function sourceOnAssetPage(page: Page, c: Case) {
  // Scoped to the switch and matched exactly: "Source" is also a substring of
  // the sidebar's "Resources".
  const views = page.getByRole("group", { name: "Content view" });
  await views.getByRole("button", { name: "Source", exact: true }).click();
  await expect(page.locator(".cm-line").filter({ hasText: c.sourceText }).first()).toBeVisible();
  await views.getByRole("button", { name: "Preview", exact: true }).click();
  await expectDrawn(page, c, `${c.name} after returning to Preview`);
}

/** A share page's toggle shows the source, and back. */
async function sourceOnShare(page: Page, c: Case) {
  await page.getByRole("button", { name: c.sourceLabel, exact: true }).click();
  await expect(page.locator("pre").filter({ hasText: c.sourceText }).first()).toBeVisible();
  await page.getByRole("button", { name: "View Rendered", exact: true }).click();
  await expectDrawn(page, c, `${c.name} after returning to the drawing`);
}

async function checkDrawing(page: Page, c: Case) {
  const svg = page.locator(drawingSelector(c)).first();
  if (c.noLinks) {
    const markup = await svg.evaluate((el) => el.outerHTML);
    expect(markup, "the drawing carries a link").not.toMatch(/<a[\s>]|javascript:/i);
    expect(await page.locator(`[data-testid="${c.kind}-drawing"] a`).count()).toBe(0);
  }
  if (c.leavesShareARow !== undefined) {
    const rows = await svg.evaluate((el) => {
      const children = new Set(["left", "middle", "right"]);
      const ys = Array.from(el.querySelectorAll("g.node"))
        .filter((n) => children.has(n.querySelector("title")?.textContent ?? ""))
        .map((n) => Math.round(Number(n.querySelector("ellipse")?.getAttribute("cy"))));
      return new Set(ys).size;
    });
    if (c.leavesShareARow) expect(rows, "dot lays the leaves on one row").toBe(1);
    else expect(rows, "neato does not lay the leaves on one row").toBeGreaterThan(1);
  }
  if (c.marks) {
    const { selector, count } = c.marks;
    await expect.poll(() => svg.locator(selector).count(), { message: `marks ${selector}` }).toBe(count);
  }
  if (c.selection) {
    const { pick, filtered } = c.selection;
    const before = await svg.locator(filtered).count();
    await svg.locator(pick).first().click();
    await expect
      .poll(() => svg.locator(filtered).count(), { message: "the selection does not filter the second view" })
      .toBeLessThan(before);
  }
}

const READER_SURFACE = "share, signed in without edit access";

/** Whether the scene is open in Excalidraw's editor rather than read-only. */
async function sceneEditable(page: Page): Promise<boolean> {
  return (await page.locator('[data-testid="excalidraw-scene"] .App-toolbar').count()) > 0;
}

/** Draws a rectangle on the scene and saves it through the page's Save. */
async function drawAndSave(page: Page) {
  // The tool is a radio input styled under its label, so it is clicked through.
  await page.locator('[data-testid="excalidraw-scene"] .App-toolbar [data-testid="toolbar-rectangle"]').click({ force: true });
  const box = (await page.locator('[data-testid="excalidraw-scene"]').boundingBox())!;
  await page.mouse.move(box.x + box.width * 0.6, box.y + box.height * 0.6);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width * 0.8, box.y + box.height * 0.8, { steps: 8 });
  await page.mouse.up();
  await page.getByRole("button", { name: "Save", exact: true }).first().click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByText("Saved", { exact: true })).toBeVisible();
}

/** Pauses the animation and scrubs it to a frame. */
async function playback(page: Page) {
  await page.getByRole("button", { name: "Pause" }).click();
  await expect(page.getByRole("button", { name: "Play" })).toBeVisible();
  const slider = page.getByRole("slider", { name: "Frame" });
  await slider.fill("10");
  await expect(page.getByText(/^10\//)).toBeVisible();
}

async function checkKind(page: Page, c: Case, surface: string) {
  if (c.editorForOwnerOnly) {
    const owner = surface === "asset page";
    expect(await sceneEditable(page), `${c.name} on the ${surface}: editor ${owner ? "missing" : "offered"}`).toBe(owner);
  }
  if (surface !== "asset page") return;
  if (c.playback) await playback(page);
  if (c.drawAndSave) await drawAndSave(page);
}

/**
 * The source one toggle away. An authenticated share opened by the asset's
 * owner lands on the asset page, whose switch is the one to use there; a file
 * drawn on its resource page has no source toggle (sourceLabel "").
 */
async function checkSource(page: Page, c: Case) {
  if (!c.sourceLabel) return;
  if (new URL(page.url()).pathname.startsWith("/portal/assets/")) await sourceOnAssetPage(page, c);
  else await sourceOnShare(page, c);
}

for (const c of cases()) {
  test(`criterion browser: ${c.name}`, async ({ browser }) => {
    const signed = await signedIn(browser);
    const anon = await (await browser.newContext()).newPage();
    const surfaces: Array<[string, Page, string]> = [["asset page", signed, c.page ?? `/portal/assets/${c.asset}`]];
    if (c.authShare) surfaces.push(["authenticated share", signed, c.authShare]);
    if (c.publicShare) surfaces.push(["public share, signed out", anon, c.publicShare]);
    if (c.viewerShare) surfaces.push([READER_SURFACE, await signedIn(browser, "reader"), c.viewerShare]);
    const failures: string[] = [];
    for (const [surface, page, path] of surfaces) {
      const refusals = watchRefusals(page);
      await page.goto(path);
      try {
        await expectDrawn(page, c, `${c.name} on the ${surface}`);
        if (surface === "asset page") await checkDrawing(page, c);
        await checkKind(page, c, surface);
        // A reader without edit access has no Source view on the asset page.
        if (surface !== READER_SURFACE) await checkSource(page, c);
        if (refusals.length) failures.push(`${c.name} on the ${surface}: refused: ${refusals.join(" | ")}`);
      } catch (e) {
        failures.push(`${c.name} on the ${surface}: ${(e as Error).message}`);
      }
      page.removeAllListeners("console");
      page.removeAllListeners("pageerror");
    }
    expect(failures, failures.join("\n")).toEqual([]);
  });
}
