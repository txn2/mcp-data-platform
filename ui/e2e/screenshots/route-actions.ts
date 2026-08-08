import { type Page } from "@playwright/test";

/**
 * Capture actions that more than one route in the manifest performs, or that
 * are long enough to crowd it out. A route with a one-off `beforeCapture`
 * keeps it inline; these live here so the manifest stays a readable list of
 * routes rather than a file of closures.
 */

/** openShareDialog opens the asset viewer's Share dialog. */
export async function openShareDialog(page: Page): Promise<void> {
  const btn = page.locator("button:has-text('Share')").first();
  if (await btn.isVisible()) {
    await btn.click();
    await page.waitForTimeout(600);
  }
}

/**
 * openShareDialogWithRecipient opens the Share dialog and names a recipient,
 * which is the only state showing the notify checkbox and the sharer's
 * message box (#1016). It builds on openShareDialog so the two share captures
 * cannot drift apart.
 */
export async function openShareDialogWithRecipient(page: Page): Promise<void> {
  await openShareDialog(page);
  const recipient = page.locator("input[type='email']").first();
  if (await recipient.isVisible()) {
    await recipient.fill("marcus.johnson@example.com");
    await page.waitForTimeout(400);
  }
}

/**
 * openFeedbackMentionComposer opens an asset's feedback panel and starts a new
 * thread whose body carries an @-mention, which is the only state showing the
 * audience-scoped type-ahead (#627).
 */
export async function openFeedbackMentionComposer(page: Page): Promise<void> {
  const btn = page.getByRole("main").getByRole("button", { name: /Feedback/ }).first();
  if (!(await btn.isVisible())) return;
  await btn.click();
  const newBtn = page.getByRole("button", { name: "New", exact: true });
  if (!(await newBtn.isVisible())) return;
  await newBtn.click();
  await page.getByPlaceholder("Describe your feedback").fill("cc @marcus");
  await page.waitForTimeout(500);
}

/**
 * openKnowledgeGraph switches the knowledge-pages surface to its graph layout
 * and brings the canvas into the viewport: it sits below the hub header, so a
 * capture of the top of the page would show only its first third.
 */
export async function openKnowledgeGraph(page: Page): Promise<void> {
  await page
    .getByRole("button", { name: "Graph" })
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(600);
  await page
    .getByRole("application", { name: "Knowledge graph" })
    .scrollIntoViewIfNeeded({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(400);
}

/**
 * openInsightReviewDrawer switches the insights tab from its "My Insights"
 * default (cards, no table) to the reviewer "Review queue" sub-tab and opens the
 * first row's InsightDrawer. Each click may no-op when a drawer is already open
 * from the prior theme (light and dark share one page and a same-hash navigation
 * does not reload); the open drawer is what the capture wants, so failures are
 * swallowed.
 */
export async function openInsightReviewDrawer(page: Page): Promise<void> {
  await page
    .getByRole("tab", { name: /Review queue/i })
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(400);
  const row = page.locator("table tbody tr").first();
  await row.click({ timeout: 3_000 }).catch(() => {});
  await page.waitForTimeout(600);
}

/**
 * openKnowledgeGraphCorpus opens the graph and switches it to the whole-corpus
 * overview, where the detected clusters are drawn as regions.
 */
export async function openKnowledgeGraphCorpus(page: Page): Promise<void> {
  await openKnowledgeGraph(page);
  await page
    .getByRole("button", { name: "Whole corpus" })
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(900);
}

/**
 * openResourceDetail opens the resources table's revision-trail fixture, which
 * is the only one carrying both a read-activity rollup and a version history.
 */
export async function openResourceDetail(page: Page): Promise<void> {
  await page
    .getByText("SQL Style Guide", { exact: true })
    .first()
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(700);
}

/**
 * openResourceLifecycle opens the same dialog and scrolls its body to the
 * lifecycle panels. Those sit below the fold of the capped panel (#1233), so a
 * capture of the dialog as it opens shows the identity and the preview and
 * cannot reach the usage rollup, the version trail, or the prompts attaching
 * the resource -- which is what the docs prose beside this image documents.
 */
export async function openResourceLifecycle(page: Page): Promise<void> {
  await openResourceDetail(page);
  await page
    .getByTestId("resource-usage")
    .scrollIntoViewIfNeeded({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(500);
}

/**
 * openPersonaScopeTab switches a resources table to the data-engineer persona
 * scope, which is the one the fixtures populate. Both the user and admin
 * resources captures want it, so it lives here rather than twice in the
 * manifest. The tab is absent on a deployment with no persona resources, which
 * is why its visibility is checked rather than assumed.
 */
export async function openPersonaScopeTab(page: Page): Promise<void> {
  const tab = page.locator("text=data-engineer").first();
  if (await tab.isVisible()) {
    await tab.click();
    await page.waitForTimeout(500);
  }
}
