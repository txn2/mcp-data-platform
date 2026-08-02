import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the knowledge-graph view (#1162): the alternate
// layout of the knowledge corpus. Runs against MSW, whose graph handler derives
// nodes and edges from the same seeded page bodies the real backend would scan,
// so an edge here corresponds to a real citation rather than hand-authored data.

async function gotoGraph(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/knowledge/pages");
  await page.getByRole("radio", { name: "Graph" }).click();
  await expect(page.getByRole("application", { name: "Knowledge graph" })).toBeVisible();
}

/** Opens the graph and drops it into the whole-corpus overview. */
async function gotoCorpus(page: Page): Promise<void> {
  await gotoGraph(page);
  await page.getByRole("radio", { name: "Whole corpus" }).click();
}

/** node returns the graph vertex with the given accessible name. */
function node(page: Page, name: string) {
  return page.getByRole("application", { name: "Knowledge graph" }).getByLabel(name, { exact: true });
}

function inspector(page: Page) {
  return page.getByRole("complementary");
}

/**
 * waitForSettledLayout blocks until the force simulation has stopped moving the
 * named node. The layout animates, so a test that measures a node and then
 * presses where it was can find it has drifted out from under the pointer — on a
 * slow runner that turns a drag into a press on empty canvas.
 */
async function waitForSettledLayout(page: Page, name: string): Promise<void> {
  const target = node(page, name);
  let previous: string | null = null;
  await expect
    .poll(
      async () => {
        const current = await target.getAttribute("transform");
        const unchanged = current !== null && current === previous;
        previous = current;
        return unchanged;
      },
      { timeout: 20_000, intervals: [250] },
    )
    .toBe(true);
}

/** parseTranslate reads the x/y out of a `translate(x,y)` transform attribute. */
function parseTranslate(transform: string | null): { x: number; y: number } {
  const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(transform ?? "");
  if (!m) throw new Error(`not a translate transform: ${transform}`);
  return { x: Number(m[1]), y: Number(m[2]) };
}

test.describe("Knowledge graph", () => {
  test("toggles between cards and graph, preserving the tag filter and search", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/knowledge/pages");
    await page.getByRole("button", { name: /^finance/ }).click();
    await page.getByPlaceholder("Search knowledge by content...").fill("revenue");

    await page.getByRole("radio", { name: "Graph" }).click();

    await expect(page.getByRole("application", { name: "Knowledge graph" })).toBeVisible();
    await expect(page.getByRole("button", { name: /^finance/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(page.getByPlaceholder("Find nodes in the graph...")).toHaveValue("revenue");

    await page.getByRole("radio", { name: "Cards" }).click();
    await expect(page.getByRole("application", { name: "Knowledge graph" })).toHaveCount(0);
    await expect(page.getByPlaceholder("Search knowledge by content...")).toHaveValue("revenue");
  });

  test("opens on a neighbourhood, not the whole corpus", async ({ page }) => {
    await gotoGraph(page);

    // The summary names the node the view opened on and states that it is
    // showing a slice, which is the whole point of the default view.
    await expect(page.getByText(/^Around /)).toBeVisible();
    const summary = await page.getByText(/^Around /).innerText();
    const [, shown, total] = /(\d+) of (\d+) nodes/.exec(summary) ?? [];
    expect(Number(shown)).toBeLessThan(Number(total));
  });

  test("reports the clusters and bridges it measured", async ({ page }) => {
    await gotoGraph(page);

    await expect(page.getByText(/\d+ clusters \(modularity /)).toBeVisible();
    await expect(inspector(page).getByText("Bridges")).toBeVisible();
    // The view opens on the corpus's strongest bridge, so that is what its own
    // readout must say — a below-count percentile would call it "top 100%".
    await expect(inspector(page).getByText(/\d+ paths · strongest in view/)).toBeVisible();

    // A lesser bridge gets a band instead.
    await node(page, "Page: Revenue Definition").click();
    await expect(inspector(page).getByText(/paths · top \d+%|nothing/)).toBeVisible();
  });

  test("widens the neighbourhood one hop at a time", async ({ page }) => {
    await gotoGraph(page);
    const shown = async () =>
      Number(/(\d+) of \d+ nodes/.exec(await page.getByText(/^Around /).innerText())?.[1]);

    await page.getByRole("radio", { name: "1" }).click();
    const atOne = await shown();
    await page.getByRole("radio", { name: "3" }).click();

    expect(await shown()).toBeGreaterThan(atOne);
  });

  test("drops back to the whole corpus on demand", async ({ page }) => {
    await gotoCorpus(page);

    await expect(page.getByText(/^\d+ nodes, \d+ references/)).toBeVisible();
    await expect(page.getByText(/^Around /)).toHaveCount(0);
  });

  test("draws pages and referenced entities as typed, named nodes", async ({ page }) => {
    await gotoCorpus(page);

    await expect(node(page, "Page: Net Revenue Definition")).toBeVisible();
    // Entity nodes carry a resolved display name, not a raw URN.
    await expect(node(page, "Asset: Q4 Revenue Dashboard")).toBeVisible();
    await expect(node(page, "Connection: acme-warehouse (trino)")).toBeVisible();
    await expect(node(page, "Catalog: iceberg.retail.daily_sales")).toBeVisible();
  });

  test("shows a page with no references as an isolated node", async ({ page }) => {
    await gotoCorpus(page);
    // The Fiscal Calendar seed body cites nothing, so it has no edges. It must
    // still be a vertex: an isolated page is a finding, not something to hide.
    await expect(node(page, "Page: Fiscal Calendar")).toBeVisible();
  });

  test("selecting a node inspects it in place instead of navigating away", async ({ page }) => {
    await gotoCorpus(page);

    await node(page, "Page: Fiscal Calendar").click();

    await expect(page).toHaveURL(/\/knowledge\/pages$/);
    await expect(inspector(page).getByText("Fiscal Calendar")).toBeVisible();
    await expect(inspector(page).getByText("References out")).toBeVisible();
  });

  test("opens the page from the inspector's explicit action", async ({ page }) => {
    await gotoCorpus(page);

    await node(page, "Page: Fiscal Calendar").click();
    await inspector(page).getByRole("button", { name: /Open/ }).click();

    await expect(page.getByRole("heading", { name: "Fiscal Calendar", level: 1 }).first()).toBeVisible();
  });

  test("opens an entity node at its portal home", async ({ page }) => {
    await gotoCorpus(page);

    await node(page, "Asset: Q4 Revenue Dashboard").click();
    await inspector(page).getByRole("button", { name: /Open/ }).click();

    await expect(page).toHaveURL(/\/portal\/assets\/ast-001/);
  });

  test("opens a catalog node in the Catalog tab", async ({ page }) => {
    await gotoCorpus(page);

    await node(page, "Catalog: iceberg.retail.daily_sales").click();
    // The URN is the identifier the label is derived from; without it on screen
    // there is nothing the reader can search or act on.
    await expect(
      inspector(page).getByText("urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"),
    ).toBeVisible();

    await inspector(page).getByRole("button", { name: /Open/ }).click();

    await expect(page).toHaveURL(/\/knowledge\/catalog\?urn=/);
  });

  test("says when a page cites a dataset the catalog does not have", async ({ page }) => {
    await gotoCorpus(page);

    await node(page, "Catalog: iceberg.retail.daily_sales").click();

    // The seeded pages cite datasets that are not in the seeded catalog, which
    // is a real data-quality finding the graph should state rather than draw as
    // an ordinary live node.
    await expect(inspector(page).getByText(/Not found in/)).toBeVisible();
  });

  test("offers no open action for a node with no portal home", async ({ page }) => {
    await gotoCorpus(page);

    await node(page, "Connection: acme-warehouse (trino)").click();

    await expect(inspector(page).getByText("acme-warehouse (trino)")).toBeVisible();
    await expect(inspector(page).getByRole("button", { name: /Open/ })).toHaveCount(0);
  });

  test("re-roots the view on the inspector's focus action", async ({ page }) => {
    await gotoCorpus(page);

    await node(page, "Page: Fiscal Calendar").click();
    await inspector(page).getByRole("button", { name: /Focus/ }).click();

    await expect(page.getByText("Around Fiscal Calendar")).toBeVisible();
  });

  test("traces the shortest path between two nodes", async ({ page }) => {
    await gotoCorpus(page);

    await node(page, "Page: Net Revenue Definition").click();
    await inspector(page).getByRole("button", { name: /Path from/ }).click();
    await expect(inspector(page).getByText(/Click another node/)).toBeVisible();

    await node(page, "Page: Inventory Snapshot Grain").click();

    await expect(inspector(page).getByText(/hops? apart/)).toBeVisible();
  });

  test("navigates the corpus through the inspector's neighbour lists", async ({ page }) => {
    await gotoCorpus(page);

    await node(page, "Page: Net Revenue Definition").click();
    await inspector(page).getByRole("button", { name: "Revenue Definition" }).click();

    // The clicked neighbour becomes the inspected node, without leaving the graph.
    await expect(inspector(page).getByRole("heading", { name: "Revenue Definition" })).toBeVisible();
  });

  test("hover highlights a node's neighbourhood and dims the rest", async ({ page }) => {
    await gotoCorpus(page);
    const hub = node(page, "Asset: Q4 Revenue Dashboard");
    const neighbour = node(page, "Page: Net Revenue Definition");
    const unrelated = node(page, "Page: Fiscal Calendar");

    await hub.hover();

    await expect(neighbour).toHaveAttribute("opacity", "1");
    await expect(unrelated).toHaveAttribute("opacity", "0.15");
  });

  test("dragging a node moves it and leaves it where it is dropped", async ({ page }) => {
    await gotoGraph(page);
    const target = node(page, "Page: Net Revenue Definition");
    // The layout must be still before the node is measured and pressed, or the
    // press lands where the node WAS and the drag never starts.
    await waitForSettledLayout(page, "Page: Net Revenue Definition");

    // hover() presses on a point that actually hits the node: its bounding box
    // spans the mark and the label below it, so the box centre can fall between
    // the two. It also SCROLLS the node into view, which moves the canvas
    // relative to the viewport — so the canvas is measured after the hover, not
    // before, or every drop coordinate is off by the scroll distance.
    await target.hover();
    const svgBox = await page
      .getByRole("application", { name: "Knowledge graph" })
      .boundingBox();
    expect(svgBox).not.toBeNull();

    // A fixed destination well inside the canvas, so the pointer cannot leave
    // the surface mid-drag (which would end the drag early) whatever the
    // measured canvas width is.
    const DROP_X = 120;
    const DROP_Y = 140;
    await page.mouse.down();
    await page.mouse.move(svgBox!.x + DROP_X, svgBox!.y + DROP_Y, { steps: 10 });
    await page.mouse.up();

    // The node goes exactly where it was dropped, not merely somewhere else.
    const dropped = parseTranslate(await target.getAttribute("transform"));
    expect(dropped.x).toBeCloseTo(DROP_X, 0);
    expect(dropped.y).toBeCloseTo(DROP_Y, 0);

    // The simulation keeps running after the drop; a dragged node is pinned, so
    // it must stay put rather than drift back into the pack.
    await page.waitForTimeout(2000);
    const settled = parseTranslate(await target.getAttribute("transform"));
    expect(settled.x).toBeCloseTo(DROP_X, 0);
    expect(settled.y).toBeCloseTo(DROP_Y, 0);
  });

  test("the canvas measures its container instead of using a fixed width", async ({ page }) => {
    // The container is only ~900px at the default 1440px viewport, which is
    // exactly the fallback width — so this narrows the window first, where a
    // canvas that never measured would overflow its container instead of fitting.
    await page.setViewportSize({ width: 1100, height: 900 });
    await gotoGraph(page);

    const svgBox = await page
      .getByRole("application", { name: "Knowledge graph" })
      .boundingBox();
    expect(svgBox).not.toBeNull();
    expect(svgBox!.width).toBeLessThan(900);
  });

  test("wheel zoom is proportional to the gesture, not one jump per event", async ({ page }) => {
    await gotoGraph(page);
    const layer = page.getByRole("application", { name: "Knowledge graph" }).locator("g").first();
    const scale = async () =>
      Number(/scale\(([\d.]+)\)/.exec((await layer.getAttribute("transform")) ?? "")?.[1]);

    const svgBox = await page
      .getByRole("application", { name: "Knowledge graph" })
      .boundingBox();
    // Aim near the canvas's top-left, not its centre: the canvas is taller than
    // the remaining viewport, so its midpoint can sit below the fold where the
    // pointer lands on nothing at all.
    await page.mouse.move(svgBox!.x + 120, svgBox!.y + 120);

    // One trackpad tick must be a nudge.
    await page.mouse.wheel(0, -10);
    const afterOne = await scale();
    expect(afterOne).toBeGreaterThan(1);
    expect(afterOne).toBeLessThan(1.05);

    // A whole two-finger swipe is ~30 events. A fixed step per event compounded
    // this to 1.2^30 and pinned the view at the maximum zoom instantly.
    for (let i = 0; i < 29; i++) await page.mouse.wheel(0, -10);
    const afterSwipe = await scale();
    expect(afterSwipe).toBeGreaterThan(1.2);
    expect(afterSwipe).toBeLessThan(2.5);

    // And the reverse gesture returns roughly where it started.
    for (let i = 0; i < 30; i++) await page.mouse.wheel(0, 10);
    expect(await scale()).toBeCloseTo(1, 1);
  });

  test("zoom controls scale the graph", async ({ page }) => {
    await gotoGraph(page);
    const layer = page.getByRole("application", { name: "Knowledge graph" }).locator("g").first();
    const before = await layer.getAttribute("transform");

    await page.getByRole("button", { name: "Zoom in" }).click();
    await expect(layer).not.toHaveAttribute("transform", before ?? "");

    await page.getByRole("button", { name: "Fit" }).click();
    await expect(layer).toHaveAttribute("transform", "translate(0,0) scale(1)");
  });

  test("the type filter removes a whole class of node", async ({ page }) => {
    await gotoCorpus(page);
    await expect(node(page, "Connection: acme-warehouse (trino)")).toBeVisible();

    await page
      .getByRole("group", { name: "Node types" })
      .getByRole("button", { name: /^Connection/ })
      .click();

    await expect(node(page, "Connection: acme-warehouse (trino)")).toHaveCount(0);
    // Pages are never hidden by the type filter.
    await expect(node(page, "Page: Fiscal Calendar")).toBeVisible();
  });

  test("the search box focuses matching nodes instead of refiltering", async ({ page }) => {
    await gotoCorpus(page);
    await page.getByPlaceholder("Find nodes in the graph...").fill("fiscal");

    // The match is lit and everything else dims; nothing is removed.
    await expect(node(page, "Page: Fiscal Calendar")).toHaveAttribute("opacity", "1");
    await expect(node(page, "Page: Net Revenue Definition")).toHaveAttribute("opacity", "0.15");
    await expect(node(page, "Page: Net Revenue Definition")).toBeVisible();
  });

  // The empty corpus and the truncation notice are covered by
  // KnowledgeGraphView.test.tsx: MSW answers from a service worker, so a
  // Playwright route override never sees the request, and neither state is
  // reachable from the seeded corpus (30 pages, well under the endpoint's cap).
});
