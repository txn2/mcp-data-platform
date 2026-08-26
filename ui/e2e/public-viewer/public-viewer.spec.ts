import { test, expect, type Page } from "@playwright/test";

// The public viewer's Content-Security-Policy is enforced by the browser and by
// nothing else, so a string assertion in Go proves only what the header says.
// These tests render each content family under the real policy and fail on the
// console message a blocked resource produces.
//
// Tokens are the public shares in dev/seed.sql: one per client-rendered family
// plus the collection.
const TOKENS = {
  html: "tok-revenue-dash-public",
  jsx: "tok-store-compare-public",
  markdown: "tok-pipeline-arch-public",
  svg: "tok-regional-heatmap-public",
  collection: "tok-q3-exec-review-public",
};

// The content-viewer bundle is a gitignored build artifact embedded into the
// binary at compile time, and the page omits the viewer <script> entirely when
// the binary was built without it. Every family case would then time out
// waiting for content that no code is there to render, so the run stops here
// instead with the reason.
test.beforeAll(async ({ playwright }) => {
  const baseURL = test.info().project.use.baseURL;
  const request = await playwright.request.newContext({ baseURL });
  const response = await request.get(`/portal/view/${TOKENS.html}`);
  const html = await response.text();
  await request.dispose();

  expect(
    response.status(),
    `${baseURL} did not serve the share page. Start a stack with \`make dev\` and point PUBLIC_VIEWER_BASE_URL at it.`,
  ).toBe(200);
  expect(
    /<script type="module" src="\/portal\/view\/_assets\//.test(html),
    "the server carries no content-viewer bundle: it was built with internal/contentviewer/dist empty. " +
      "Run `make frontend-build`, then restart the server so the rebuilt bundle is embedded.",
  ).toBe(true);
});

/** Console text a CSP refusal produces, in either the page or a child frame. */
function isCSPRefusal(text: string): boolean {
  return /Content Security Policy|Refused to/i.test(text);
}

/**
 * Collect CSP refusals from a page and every frame under it. Returned array is
 * live: read it after the interaction under test.
 */
function watchCSP(page: Page): string[] {
  const refusals: string[] = [];
  page.on("console", (m) => {
    if (isCSPRefusal(m.text())) refusals.push(m.text());
  });
  page.on("pageerror", (e) => {
    if (isCSPRefusal(e.message)) refusals.push(e.message);
  });
  return refusals;
}

/**
 * The artifact frame of an HTML or JSX share. Resolved as a frame locator so
 * assertions re-resolve while the blob: document is still loading.
 */
function artifactFrame(page: Page) {
  return page.frameLocator("iframe").first();
}

test.describe("public viewer content families", () => {
  test("HTML renders in its sandboxed frame with no blocked resource", async ({ page }) => {
    const refusals = watchCSP(page);
    await page.goto(`/portal/view/${TOKENS.html}`, { waitUntil: "networkidle" });

    const frame = artifactFrame(page);
    await expect(frame.locator("body")).toContainText("Weekly Revenue Dashboard");
    expect(refusals, refusals.join("\n")).toHaveLength(0);
  });

  test("JSX transpiles, resolves its esm.sh imports and mounts", async ({ page }) => {
    const refusals = watchCSP(page);
    await page.goto(`/portal/view/${TOKENS.jsx}`, { waitUntil: "networkidle" });

    const frame = artifactFrame(page);
    // The component mounted: react and react-dom loaded through the import map
    // under script-src https:, and the module ran without 'unsafe-eval'.
    await expect(frame.locator("body")).toContainText("Store Performance Comparison");
    await expect(frame.locator("body")).toContainText("Downtown Flagship");
    expect(refusals, refusals.join("\n")).toHaveLength(0);
  });

  test("markdown renders inline in the page", async ({ page }) => {
    const refusals = watchCSP(page);
    await page.goto(`/portal/view/${TOKENS.markdown}`, { waitUntil: "networkidle" });

    await expect(page.locator("#content-root")).toContainText("Data Pipeline Architecture");
    expect(refusals, refusals.join("\n")).toHaveLength(0);
  });

  test("SVG renders inline in the page", async ({ page }) => {
    const refusals = watchCSP(page);
    await page.goto(`/portal/view/${TOKENS.svg}`, { waitUntil: "networkidle" });

    await expect(page.locator("#content-root")).toContainText("Regional Sales Heatmap");
    expect(refusals, refusals.join("\n")).toHaveLength(0);
  });

  test("a collection item opens its viewer in a same-origin frame", async ({ page }) => {
    const refusals = watchCSP(page);
    await page.goto(`/portal/view/${TOKENS.collection}`, { waitUntil: "networkidle" });

    // Opening an item loads the single-asset viewer under frame-src 'self',
    // which then renders an HTML item in its own blob: frame. Assert on the
    // artifact's own text, two frames deep: the item viewer's chrome renders
    // before its content does, so anything less passes on a blank artifact.
    await page.getByText("Q3 Financial Summary", { exact: false }).first().click();
    const artifact = page
      .frameLocator("iframe[src*='/items/']")
      .frameLocator("iframe");
    await expect(artifact.locator("body")).toContainText("ACME Corporation");
    expect(refusals, refusals.join("\n")).toHaveLength(0);
  });
});

// What the page loads is the whole point of the split (#1355), and it is a
// property of the built chunk graph rather than of any one source file, so it
// is asserted here against a real browser fetching a real share.
test.describe("what a share page loads", () => {
  /** Viewer chunk filenames the page fetched, in order. */
  function watchChunks(page: Page): string[] {
    const chunks: string[] = [];
    page.on("request", (r) => {
      const url = r.url();
      const at = url.indexOf("/portal/view/_assets/");
      if (at >= 0) chunks.push(url.slice(at + "/portal/view/_assets/".length));
    });
    return chunks;
  }

  test("a markdown share fetches the markdown viewer and not the others", async ({ page }) => {
    const chunks = watchChunks(page);
    await page.goto(`/portal/view/${TOKENS.markdown}`, { waitUntil: "networkidle" });
    await expect(page.locator("#content-root")).toContainText("Data Pipeline Architecture");

    const loaded = chunks.join(" ");
    expect(loaded, "the entry chunk was never fetched").toContain("content-viewer-entry");
    expect(loaded, "the markdown viewer was never fetched").toContain("MarkdownRenderer");
    // The families this asset is not. Each was in the single bundle every
    // share used to carry.
    expect(loaded).not.toContain("CodeRenderer");
    expect(loaded).not.toContain("JsxRenderer");
    expect(loaded).not.toContain("JsonRenderer");
  });

  test("the chunks are cacheable, so a second share costs no JavaScript", async ({ page, request }) => {
    const chunks = watchChunks(page);
    await page.goto(`/portal/view/${TOKENS.markdown}`, { waitUntil: "networkidle" });
    const entry = chunks.find((c) => c.startsWith("content-viewer-entry"));
    expect(entry, "no entry chunk was fetched").toBeTruthy();

    const res = await request.get(`/portal/view/_assets/${entry}`);
    expect(res.status()).toBe(200);
    expect(res.headers()["cache-control"]).toContain("immutable");
    expect(res.headers()["content-type"]).toContain("javascript");
  });

  test("the asset route serves the bundle and nothing else under it", async ({ request }) => {
    // vite's manifest describes the graph; it is not part of what a browser
    // loads.
    for (const path of [
      "/portal/view/_assets/.vite/manifest.json",
      "/portal/view/_assets/missing-0000.js",
    ]) {
      const res = await request.get(path);
      expect(res.status(), `${path} was served`).toBe(404);
    }

    // Traversal, asserted as "not served" rather than on a status. The client
    // resolves ".." (and "%2e%2e") before the request leaves it, so the server
    // is asked for /etc/passwd and the portal's auth gate answers 401 -- this
    // route never sees the request. What matters either way is that no file
    // comes back.
    const traversal = await request.get("/portal/view/_assets/../../../etc/passwd");
    expect(traversal.status(), "traversal was served").not.toBe(200);
    expect(await traversal.text()).not.toContain("root:");
  });
});

test.describe("policy", () => {
  test("the served header denies plaintext script and runtime eval", async ({ page }) => {
    const response = await page.goto(`/portal/view/${TOKENS.html}`);
    const csp = response?.headers()["content-security-policy"] ?? "";
    expect(csp, "the viewer served no CSP at all").not.toEqual("");

    const scriptSrc = csp
      .split(";")
      .map((d) => d.trim())
      .find((d) => d.startsWith("script-src"));
    expect(scriptSrc).toBeTruthy();
    expect(scriptSrc!.split(/\s+/)).not.toContain("http:");
    expect(scriptSrc!.split(/\s+/)).not.toContain("'unsafe-eval'");
  });

  // The policy reaches artifacts by inheritance: a blob: document takes the
  // policy of the document that created it. These two cases assert on that
  // inherited policy, which is the only thing standing between a stored
  // artifact and the network.
  test("an artifact cannot pull script over plain http", async ({ page }) => {
    // The refusal itself is the assertion. "the script did not run" is not:
    // a host that simply fails to resolve produces the same page text, so
    // this case would report green offline against a reverted policy.
    const refusals = watchCSP(page);
    await page.goto(`/portal/view/${TOKENS.html}`);
    const out = await renderInArtifactFrame(
      page,
      `<!DOCTYPE html><html><body><div id="out">PENDING</div>
<script src="http://cdn.jsdelivr.net/npm/canvas-confetti@1.9.3/dist/confetti.browser.min.js"></script>
<script>document.getElementById('out').textContent =
  (typeof confetti === 'function') ? 'HTTP-SCRIPT-RAN' : 'HTTP-SCRIPT-BLOCKED';</script>
</body></html>`,
      3000,
    );
    expect(out).toContain("HTTP-SCRIPT-BLOCKED");
    expect(
      refusals.filter((r) => /Content Security Policy/i.test(r) && r.includes("http://cdn.jsdelivr.net")),
      `no CSP refusal named the plaintext script; refusals seen:\n${refusals.join("\n")}`,
    ).not.toHaveLength(0);
  });

  test("an artifact cannot compile source at runtime", async ({ page }) => {
    await page.goto(`/portal/view/${TOKENS.html}`);
    const out = await renderInArtifactFrame(
      page,
      `<!DOCTYPE html><html><body><div id="out">PENDING</div>
<script>
try { var f = new Function('return 41 + 1'); document.getElementById('out').textContent = 'EVAL-RAN-' + f(); }
catch (e) { document.getElementById('out').textContent = 'EVAL-BLOCKED'; }
</script></body></html>`,
      1500,
    );
    expect(out).toContain("EVAL-BLOCKED");
  });
});

// A declared reference is rewritten into the served content as an absolute URL
// under /portal/refs/, and whether it loads is decided by the policy of the
// frame the artifact renders in and by nothing else (#1474, #1488, #1494). The
// seeded HTML and JSX shares both name the brand mark as a reference, so these
// render each family under its real policy.
test.describe("asset references", () => {
  /** The referenced image inside the artifact frame, once it has been served. */
  function referencedImage(page: Page) {
    return artifactFrame(page).locator("img[src*='/portal/refs/']");
  }

  async function imageRendered(page: Page): Promise<boolean> {
    return referencedImage(page).evaluate(
      (img) => (img as HTMLImageElement).complete && (img as HTMLImageElement).naturalWidth > 0,
    );
  }

  test("an HTML share renders its referenced image", async ({ page }) => {
    const refusals = watchCSP(page);
    await page.goto(`/portal/view/${TOKENS.html}`, { waitUntil: "networkidle" });

    await expect(referencedImage(page)).toHaveCount(1);
    expect(await imageRendered(page), "the referenced image did not load").toBe(true);
    expect(refusals, refusals.join("\n")).toHaveLength(0);
  });

  test("a JSX share renders its referenced image", async ({ page }) => {
    const refusals = watchCSP(page);
    await page.goto(`/portal/view/${TOKENS.jsx}`, { waitUntil: "networkidle" });

    await expect(referencedImage(page)).toHaveCount(1);
    expect(await imageRendered(page), "the referenced image did not load").toBe(true);
    expect(refusals, refusals.join("\n")).toHaveLength(0);
  });

  // The widening is the reference route and nothing else: the artifact reaches
  // that path on the platform origin and no other path on it.
  //
  // The two halves have to be read together. The reference route succeeding is
  // what establishes that the policy the frame INHERITS from the page admits a
  // same-origin request at all; the portal API being refused on that same
  // origin is therefore attributable to the frame's own meta policy, which is
  // the layer this PR narrows.
  test("a JSX artifact reaches the reference route and no other platform path", async ({ page }) => {
    await page.goto(`/portal/view/${TOKENS.jsx}`, { waitUntil: "networkidle" });
    const base = test.info().project.use.baseURL!;

    const frame = page.frames().find((f) => f.url().startsWith("blob:"));
    expect(frame, "the JSX artifact frame never rendered").toBeTruthy();

    // Allowed by the policy: the request is made and answered. The token is
    // nonsense, so the answer is a 404 rather than a file, which is the point
    // -- a CSP refusal never reaches the network at all.
    const refRoute = await frame!.evaluate(
      (url) => fetch(url).then((r) => `STATUS-${r.status}`).catch((e) => `BLOCKED-${e}`),
      `${base}/portal/refs/no-such-asset/no-such-token`,
    );
    expect(refRoute, "the reference route was refused by the frame's policy").toContain("STATUS-");

    // Denied: the same origin, a different path.
    const api = await frame!.evaluate(
      (url) => fetch(url).then((r) => `STATUS-${r.status}`).catch((e) => `BLOCKED-${e}`),
      `${base}/api/v1/portal/me`,
    );
    expect(api, "the artifact reached the portal API").toContain("BLOCKED-");
  });
});

/**
 * Render a document the way the HTML renderer does — a blob: URL in an
 * iframe sandboxed with allow-scripts and no allow-same-origin — inside the
 * live viewer page, so it runs under the page's inherited policy. Returns the
 * frame's text.
 */
async function renderInArtifactFrame(page: Page, html: string, settleMs: number): Promise<string> {
  await page.evaluate(
    async ([doc, ms]) => {
      const url = URL.createObjectURL(new Blob([doc as string], { type: "text/html;charset=utf-8" }));
      const frame = document.createElement("iframe");
      frame.id = "probe-frame";
      frame.setAttribute("sandbox", "allow-scripts");
      frame.src = url;
      document.body.appendChild(frame);
      await new Promise((resolve) => setTimeout(resolve, ms as number));
    },
    [html, settleMs] as [string, number],
  );
  const frame = page.frameLocator("#probe-frame");
  return (await frame.locator("#out").textContent()) ?? "";
}
