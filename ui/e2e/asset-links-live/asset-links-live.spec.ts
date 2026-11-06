import { test, expect, type Browser, type BrowserContext, type Page } from "@playwright/test";

// #2501: a link to a website or an email address in an HTML, JSX or Markdown
// asset opens in a new tab, from the asset page signed in and from its public
// share signed out, and nothing else opens. Each case is one asset
// test/acceptance/issue_2501_test.go saved, named in LINKS_CASES with its
// public share and the check to run on it.
//
// The assets it saves carry these elements (issue_2501_test.go writes them):
//   HTML: #plain (https link), #blank (target=_blank), #opener (window.open on
//         click), #mail (mailto), #loop (five window.open on one click), #js
//         (a " javascript:" link); on load the document calls window.open five
//         times, posts forged javascript: and data: messages, then posts
//         {type: "links-test-loaded"}.
//   JSX:  #plain, #mail, #js, and the same unprompted opens and marker on mount.
//   Markdown: a link "the site" to https://example.com/md.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; this suite is run by test/acceptance (issue_2501)`);
  return v;
};

type Check = "opens" | "mailto" | "refuses" | "unprompted" | "noopener";
type Kind = "html" | "jsx" | "markdown";
type Case = { check: Check; kind: Kind; asset: string; publicShare: string };

const SITE = "https://example.com";

/** Where the portal keeps an API-key sign-in (API_KEY_STORAGE in ui/src/stores/auth.ts). */
const API_KEY_STORAGE = "mcp-portal-api-key";

const FRAME: Record<Exclude<Kind, "markdown">, string> = {
  html: 'iframe[title="HTML Preview"]',
  jsx: 'iframe[title="JSX Preview"]',
};

/** The links each kind opens, by the element clicked and the address that opens. */
const OPENS: Record<Kind, { id: string; url: string }[]> = {
  html: [
    { id: "#plain", url: `${SITE}/plain` },
    { id: "#blank", url: `${SITE}/blank` },
    { id: "#opener", url: `${SITE}/opened` },
  ],
  jsx: [{ id: "#plain", url: `${SITE}/jsx` }],
  markdown: [{ id: "the site", url: `${SITE}/md` }],
};

const MAILTO: Record<Exclude<Kind, "markdown">, string> = {
  html: "mailto:someone@example.com?subject=Hi",
  jsx: "mailto:someone@example.com",
};

/**
 * Installed in every page before its own script: records each window.open the
 * viewer page makes, and notes the asset's "loaded" message. Messages from one
 * frame arrive in the order sent, so once it is noted every message the asset
 * sent before it has been handled. Init scripts run without a user gesture.
 */
const RECORDER = `(() => {
  if (window.top !== window) return;
  window.__opened = [];
  window.__loaded = false;
  const open = window.open.bind(window);
  window.open = (url, target, features) => { window.__opened.push(String(url)); return open(url, target, features); };
  window.addEventListener("message", (e) => { if (e.data && e.data.type === "links-test-loaded") window.__loaded = true; });
})()`;

/**
 * Runs an expression in the page WITHOUT a user gesture. Playwright's own
 * navigation and evaluation give a page a current activation the reader never
 * made, and the rule under test is that nothing opens without the reader's
 * click, so the page is loaded and read over CDP.
 */
async function quietly<T>(page: Page, expression: string): Promise<T> {
  const cdp = await page.context().newCDPSession(page);
  const res = (await cdp.send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true, userGesture: false })) as {
    result: { value: T };
    exceptionDetails?: unknown;
  };
  await cdp.detach();
  expect(res.exceptionDetails, expression).toBeUndefined();
  return res.result.value;
}

const opened = (page: Page) => quietly<string[]>(page, "window.__opened");

/** A stand-in for the site every link names: it reports its opener and referrer, and the Referer it was sent. */
async function stubSite(context: BrowserContext): Promise<string[]> {
  const referers: string[] = [];
  await context.route(`${SITE}/**`, (route) => {
    referers.push(route.request().headers()["referer"] ?? "");
    return route.fulfill({
      contentType: "text/html",
      body: `<!DOCTYPE html><title>site</title><p id="ref"></p><script>document.getElementById("ref").textContent = JSON.stringify({ opener: window.opener === null, referrer: document.referrer });</script>`,
    });
  });
  return referers;
}

async function signIn(page: Page): Promise<void> {
  await page.goto("/portal/");
  const nav = page.locator("nav");
  const apiKey = page.locator('input[placeholder="X-API-Key"]');
  const which = await Promise.race([
    nav.waitFor({ state: "visible", timeout: 30_000 }).then(() => "nav" as const),
    apiKey.waitFor({ state: "visible", timeout: 30_000 }).then(() => "login" as const),
  ]);
  if (which === "login") {
    await apiKey.fill(env("LINKS_API_KEY"));
    await page.getByRole("button", { name: /Sign in with API key/i }).click();
    await nav.waitFor({ state: "visible", timeout: 30_000 });
  }
}

type Surface = { name: string; path: (c: Case) => string; signedIn: boolean };

const SURFACES: Surface[] = [
  { name: "the asset page signed in", path: (c) => `/portal/assets/${c.asset}`, signedIn: true },
  { name: "the public share signed out", path: (c) => c.publicShare, signedIn: false },
];

/**
 * The case's page in a tab of a fresh context, loaded over CDP so the tab
 * carries no activation, with the stand-in site routed.
 */
async function openCase(browser: Browser, surface: Surface, c: Case) {
  const context = await browser.newContext();
  const referers = await stubSite(context);
  await context.addInitScript(RECORDER);
  if (surface.signedIn) {
    // The portal keeps an API-key sign-in in sessionStorage, which is per
    // tab, and Chrome carries a click's activation across a same-origin
    // navigation in the same tab. So the key is signed in on one tab, read
    // back from where the portal stored it, and handed to the case's tab's
    // sessionStorage before the portal's script runs, which validates it as
    // it does on any reload.
    const signInPage = await context.newPage();
    await signIn(signInPage);
    const stored = await quietly<string | null>(signInPage, `sessionStorage.getItem(${JSON.stringify(API_KEY_STORAGE)})`);
    expect(stored, "the sign-in stored no API key").toBe(env("LINKS_API_KEY"));
    await signInPage.close();
    const origin = new URL(env("LINKS_BASE_URL")).origin;
    await context.addInitScript(
      `if (window.top === window && location.origin === ${JSON.stringify(origin)}) sessionStorage.setItem(${JSON.stringify(API_KEY_STORAGE)}, ${JSON.stringify(stored)});`,
    );
  }
  const page = await context.newPage();
  const url = new URL(surface.path(c), env("LINKS_BASE_URL")).href;
  const cdp = await context.newCDPSession(page);
  await cdp.send("Page.navigate", { url });
  await cdp.detach();
  await expect.poll(() => quietly<boolean>(page, `document.readyState === "complete"`)).toBe(true);
  expect(await quietly<boolean>(page, "navigator.userActivation.isActive"), "the tab starts with an activation").toBe(false);
  if (c.kind === "markdown") {
    await expect.poll(() => quietly<boolean>(page, `!!document.querySelector('a[href="${SITE}/md"]')`)).toBe(true);
  } else {
    await expect.poll(() => quietly<boolean>(page, "window.__loaded"), { message: "the asset never reported it had loaded" }).toBe(true);
  }
  return { context, page, referers };
}

function target(page: Page, c: Case, id: string) {
  if (c.kind === "markdown") return page.getByRole("link", { name: id });
  return page.frameLocator(FRAME[c.kind]).locator(id);
}

async function nextTab(context: BrowserContext, action: () => Promise<void>): Promise<Page> {
  const [tab] = await Promise.all([context.waitForEvent("page"), action()]);
  await tab.waitForLoadState("domcontentloaded");
  return tab;
}

type Opened = Awaited<ReturnType<typeof openCase>>;

/** The kinds that are framed: the checks that click inside a frame need one. */
function framed(c: Case): Exclude<Kind, "markdown"> {
  if (c.kind === "markdown") throw new Error(`the ${c.check} check is for an HTML or JSX asset`);
  return c.kind;
}

const CHECKS: Record<Check, (o: Opened, c: Case, pageURL: string) => Promise<void>> = {
  async opens({ context, page }, c, pageURL) {
    for (const link of OPENS[c.kind]) {
      const tab = await nextTab(context, () => target(page, c, link.id).click());
      expect(tab.url(), link.id).toBe(link.url);
      await tab.close();
      expect(page.url(), `${link.id} moved the page`).toBe(pageURL);
    }
    if (c.kind !== "markdown") expect(await opened(page)).toEqual(OPENS[c.kind].map((l) => l.url));
  },

  async mailto({ page }, c, pageURL) {
    const kind = framed(c);
    await target(page, c, "#mail").click();
    await expect.poll(() => opened(page)).toEqual([MAILTO[kind]]);
    expect(page.url()).toBe(pageURL);
  },

  async refuses({ context, page }, c, pageURL) {
    framed(c);
    // The asset posted javascript: and data: messages on load.
    expect(await opened(page)).toEqual([]);
    await target(page, c, "#js").click();
    await expect(target(page, c, "#plain")).toBeVisible();
    expect(await opened(page)).toEqual([]);
    expect(context.pages()).toHaveLength(1);
    expect(page.url()).toBe(pageURL);
  },

  async unprompted({ context, page }, c) {
    const kind = framed(c);
    // The asset called window.open five times on load, with no click.
    expect(await opened(page)).toEqual([]);
    expect(context.pages()).toHaveLength(1);
    if (kind !== "html") return;
    const tab = await nextTab(context, () => target(page, c, "#loop").click());
    await tab.close();
    expect(await opened(page), "one click opened more than one tab").toEqual([`${SITE}/loop0`]);
  },

  async noopener({ context, page, referers }, c, pageURL) {
    const link = OPENS[c.kind][0]!;
    const tab = await nextTab(context, () => target(page, c, link.id).click());
    const seen = JSON.parse((await tab.locator("#ref").textContent()) ?? "{}") as { opener: boolean; referrer: string };
    expect(seen.opener, "window.opener is not null").toBe(true);
    expect(seen.referrer, "the site was told the page's address").toBe("");
    expect(referers, "the request carried a Referer").toEqual([""]);
    await tab.close();
    expect(page.url()).toBe(pageURL);
  },
};

async function runCheck(browser: Browser, surface: Surface, c: Case): Promise<void> {
  const o = await openCase(browser, surface, c);
  try {
    await CHECKS[c.check](o, c, o.page.url());
  } finally {
    await o.context.close();
  }
}

const cases = (): Case[] => JSON.parse(env("LINKS_CASES")) as Case[];

for (const c of cases()) {
  for (const surface of SURFACES) {
    test(`${c.check}: a ${c.kind} asset on ${surface.name}`, async ({ browser }) => {
      await runCheck(browser, surface, c);
    });
  }
}
