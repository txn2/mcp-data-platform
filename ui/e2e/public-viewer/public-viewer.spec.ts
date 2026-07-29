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
// binary at compile time, and the page renders an empty <script> when the
// binary was built without it. Every family case would then time out waiting
// for content that no code is there to render, so the run stops here instead
// with the reason.
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
    html.includes("<script></script>"),
    "the server carries no content-viewer bundle: it was built with internal/contentviewer/dist empty. " +
      "Run `make frontend-build`, then restart the server so the rebuilt bundle is embedded.",
  ).toBe(false);
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
