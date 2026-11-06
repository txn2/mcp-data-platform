import { test, expect, type BrowserContext, type FrameLocator, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Links to websites from an HTML or JSX asset (#2501). The asset's frame stays
// sandboxed `allow-scripts allow-downloads`; a link the reader clicks to an
// http:, https: or mailto: address is posted to the portal page, which opens
// it in a new tab with noopener and noreferrer, and only while the reader's
// click is current.
//
// The mock library's own assets are rewritten in place: the content is PUT
// through the mock API from the page, and the asset page is reached by a
// client-side navigation so the in-memory mock keeps the new body. The page is
// reloaded after signing in so the sign-in click is not a current activation
// when the asset loads.

const SITE = "https://example.com";

const HTML = `<!DOCTYPE html><html><head><title>Links</title></head><body>
<p><a id="plain" href="${SITE}/plain">plain</a></p>
<p><a id="blank" href="${SITE}/blank" target="_blank">blank</a></p>
<p><a id="mail" href="mailto:someone@example.com?subject=Hi">mail</a></p>
<p><button id="opener" onclick="window.open('${SITE}/opened')">open</button></p>
<p><button id="loop" onclick="for (var i = 0; i < 5; i++) window.open('${SITE}/loop' + i)">loop</button></p>
<p><a id="jump" href="#far">jump</a></p>
<p><a id="js" href=" javascript:document.title='ran'">js</a></p>
<div style="height:3000px"></div><h2 id="far">far</h2>
<script>
if (location.hash === "") {
  // No click: a script that opens links on its own opens nothing.
  for (var i = 0; i < 5; i++) window.open("${SITE}/unprompted" + i);
  parent.postMessage({ type: "mcp:open-link", url: "${SITE}/forged" }, "*");
  parent.postMessage({ type: "mcp:open-link", url: "javascript:alert(1)" }, "*");
  parent.postMessage({ type: "mcp:open-link", url: "data:text/html,<b>x</b>" }, "*");
}
parent.postMessage({ type: "links-test-loaded" }, "*");
</script>
</body></html>`;

const JSX = `export default function Links() {
  return (
    <div>
      <a id="plain" href="${SITE}/jsx">site</a>
      <a id="mail" href="mailto:someone@example.com">mail</a>
    </div>
  );
}`;

/**
 * Runs an expression in the portal page WITHOUT a user gesture. Playwright's
 * own navigation and evaluation give the page a current activation the reader
 * never made, and the rule under test is that a frame opens nothing without
 * the reader's click, so the page is loaded and driven over CDP until the
 * asset has loaded and the unprompted opens have been answered.
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

/**
 * Every window.open the portal page makes is recorded before it is made, and
 * the frame's "loaded" message is noted. Messages from one frame arrive in the
 * order sent, so once it is noted every message the document sent before it
 * has been handled.
 */
const RECORD_OPENS = `(() => {
  window.__opened = [];
  window.__loaded = false;
  const open = window.open.bind(window);
  window.open = (url, target, features) => { window.__opened.push(String(url)); return open(url, target, features); };
  window.addEventListener("message", (e) => { if (e.data && e.data.type === "links-test-loaded") window.__loaded = true; });
})()`;

const opened = (page: Page) => quietly<string[]>(page, "window.__opened");

async function openAsset(signIn: Page, id: string, content: string, shown: string): Promise<{ page: Page; frame: FrameLocator }> {
  await authenticate(signIn);
  // A new tab loaded over CDP, so the sign-in click is no activation of it
  // (Chrome carries one across a same-origin navigation in the same tab).
  const page = await signIn.context().newPage();
  const url = new URL("/portal/", signIn.url()).href;
  await signIn.close();
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Page.navigate", { url });
  await cdp.detach();
  await expect.poll(() => quietly<boolean>(page, `document.readyState === "complete" && !!document.querySelector("nav")`)).toBe(true);
  expect(await quietly<boolean>(page, "navigator.userActivation.isActive")).toBe(false);
  await quietly(page, RECORD_OPENS);
  const status = await quietly<number>(
    page,
    `fetch(${JSON.stringify(`/api/v1/portal/assets/${id}/content`)}, { method: "PUT", body: ${JSON.stringify(content)} }).then((r) => r.status)`,
  );
  expect(status).toBe(200);
  await quietly(page, `history.pushState({}, "", ${JSON.stringify(`/portal/assets/${id}`)}); window.dispatchEvent(new PopStateEvent("popstate"))`);
  await expect.poll(() => quietly<boolean>(page, `!!document.querySelector(${JSON.stringify(shown)})`)).toBe(true);
  return { page, frame: page.frameLocator(shown) };
}

/** A new tab the next action opens, with the request that loaded it. */
async function nextTab(context: BrowserContext, action: () => Promise<void>) {
  const [tab] = await Promise.all([context.waitForEvent("page"), action()]);
  await tab.waitForLoadState("domcontentloaded");
  return tab;
}

test.beforeEach(async ({ context }) => {
  await context.route(`${SITE}/**`, (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!DOCTYPE html><title>site</title><p id="ref"></p><script>document.getElementById("ref").textContent = JSON.stringify({ opener: window.opener === null, referrer: document.referrer });</script>`,
      headers: { "x-saw-referer": route.request().headers()["referer"] ?? "" },
    }),
  );
});

test.describe("an HTML asset", () => {
  test("opens http(s) links and window.open in a new tab without opener or referrer, and keeps the portal page", async ({ page: signIn, context }) => {
    const { page, frame } = await openAsset(signIn, "ast-deck", HTML, 'iframe[title="HTML Preview"]');
    // The unprompted opens and the forged messages the document sent on load
    // were answered with nothing.
    await expect.poll(() => quietly<boolean>(page, "window.__loaded")).toBe(true);
    expect(await opened(page)).toEqual([]);
    expect(context.pages()).toHaveLength(1);
    const portalURL = page.url();

    for (const [id, path] of [["#plain", "/plain"], ["#blank", "/blank"], ["#opener", "/opened"]] as const) {
      const requests: string[] = [];
      context.on("request", (r) => {
        if (r.url().startsWith(SITE)) requests.push(r.headers()["referer"] ?? "");
      });
      const tab = await nextTab(context, () => frame.locator(id).click());
      expect(tab.url()).toBe(`${SITE}${path}`);
      const seen = JSON.parse((await tab.locator("#ref").textContent()) ?? "{}") as { opener: boolean; referrer: string };
      expect(seen.opener, `${id}: window.opener is not null`).toBe(true);
      expect(seen.referrer, `${id}: the site was told the portal address`).toBe("");
      expect(requests.every((r) => r === ""), `${id}: the request carried a Referer`).toBe(true);
      await tab.close();
      expect(page.url()).toBe(portalURL);
      await expect(frame.locator("#plain")).toBeVisible();
    }
    expect(await opened(page)).toEqual([`${SITE}/plain`, `${SITE}/blank`, `${SITE}/opened`]);
  });

  test("hands a mailto link to the browser from the portal page", async ({ page: signIn }) => {
    const { page, frame } = await openAsset(signIn, "ast-deck", HTML, 'iframe[title="HTML Preview"]');
    await expect.poll(() => quietly<boolean>(page, "window.__loaded")).toBe(true);
    expect(await opened(page)).toEqual([]);
    const portalURL = page.url();
    await frame.locator("#mail").click();
    await expect.poll(() => opened(page)).toEqual(["mailto:someone@example.com?subject=Hi"]);
    expect(page.url()).toBe(portalURL);
    await expect(frame.locator("#mail")).toBeVisible();
  });

  test("opens one tab per click, keeps #anchor links in the frame, and ignores other schemes", async ({ page: signIn, context }) => {
    const { page, frame } = await openAsset(signIn, "ast-deck", HTML, 'iframe[title="HTML Preview"]');
    await expect.poll(() => quietly<boolean>(page, "window.__loaded")).toBe(true);
    expect(await opened(page)).toEqual([]);
    const portalURL = page.url();

    const tab = await nextTab(context, () => frame.locator("#loop").click());
    await tab.close();
    expect(await opened(page)).toEqual([`${SITE}/loop0`]);

    await frame.locator("#jump").click();
    await expect(frame.locator("#far")).toBeInViewport();
    expect(await frame.locator("body").evaluate(() => location.hash)).toBe("#far");

    await frame.locator("#js").click();
    expect(await opened(page)).toEqual([`${SITE}/loop0`]);
    expect(page.url()).toBe(portalURL);
    expect(context.pages()).toHaveLength(1);
  });
});

test.describe("a JSX asset", () => {
  test("opens an https link in a new tab and hands a mailto link to the browser, keeping the portal page", async ({ page: signIn, context }) => {
    const { page, frame } = await openAsset(signIn, "ast-004", JSX, 'iframe[title="JSX Preview"]');
    const portalURL = page.url();
    await expect(frame.locator("#plain")).toBeVisible({ timeout: 30_000 });

    const tab = await nextTab(context, () => frame.locator("#plain").click());
    expect(tab.url()).toBe(`${SITE}/jsx`);
    const seen = JSON.parse((await tab.locator("#ref").textContent()) ?? "{}") as { opener: boolean; referrer: string };
    expect(seen).toEqual({ opener: true, referrer: "" });
    await tab.close();

    await frame.locator("#mail").click();
    await expect.poll(() => opened(page)).toEqual([`${SITE}/jsx`, "mailto:someone@example.com"]);
    expect(page.url()).toBe(portalURL);
  });
});

test.describe("a Markdown asset", () => {
  test("opens a link to another site in a new tab and keeps the portal page", async ({ page: signIn, context }) => {
    const { page } = await openAsset(signIn, "ast-003", `# Links\n\nSee [the site](${SITE}/md) or [mail us](mailto:someone@example.com).\n`, `[data-testid="viewer-content"] a[href="${SITE}/md"]`);
    const portalURL = page.url();
    const link = page.getByRole("link", { name: "the site" });
    await expect(link).toHaveAttribute("target", "_blank");
    await expect(link).toHaveAttribute("rel", "noopener noreferrer");
    const tab = await nextTab(context, () => link.click());
    expect(tab.url()).toBe(`${SITE}/md`);
    const seen = JSON.parse((await tab.locator("#ref").textContent()) ?? "{}") as { opener: boolean; referrer: string };
    expect(seen).toEqual({ opener: true, referrer: "" });
    await tab.close();
    expect(page.url()).toBe(portalURL);
  });
});
