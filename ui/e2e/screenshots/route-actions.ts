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
 * openInsightReviewClaim opens the review queue on the insight whose claim text
 * matches, rather than on whichever row happens to be newest. The observed
 * warehouse state (#1219) is attached to specific fixtures, so a capture of it
 * has to name the row it wants.
 */
async function openInsightReviewClaim(page: Page, claim: RegExp): Promise<void> {
  // A drawer left open by the previous capture (light and dark share one page,
  // and a hash-only navigation does not reload) covers the rows, so its overlay
  // would swallow the click that picks this capture's row.
  await page.keyboard.press("Escape");
  await page.waitForTimeout(300);
  await page
    .getByRole("tab", { name: /Review queue/i })
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(400);
  await page
    .locator("table tbody tr")
    .filter({ hasText: claim })
    .first()
    .click({ timeout: 3_000 });
  await page.waitForTimeout(600);
}

/**
 * openInsightObservedState opens a pending claim whose entity the query provider
 * resolved: the drawer states what the entity is queryable as and how many rows
 * it currently holds, beside the claim being decided.
 */
export async function openInsightObservedState(page: Page): Promise<void> {
  await openInsightReviewClaim(page, /daily_sales lags the source system/);
}

/**
 * openInsightClaimConflict opens the pending claim that states a row count the
 * table disagrees with: the same drawer, carrying the advisory marker.
 */
export async function openInsightClaimConflict(page: Page): Promise<void> {
  await openInsightReviewClaim(page, /inventory_levels holds 1140 rows/);
}

/**
 * openInsightNoRowEstimate opens a pending claim whose entity resolves on a
 * connection that does not estimate row counts: the reviewer learns the entity
 * exists and is queryable, and no count is claimed on the platform's behalf.
 */
export async function openInsightNoRowEstimate(page: Page): Promise<void> {
  await openInsightReviewClaim(page, /synced by CDC, so late edits appear here first/);
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

/**
 * openScriptReview opens the review drawer on the queued change to a script
 * that is already running, and scrolls it to the code diff. That row is the
 * capture's whole point -- a first approval has no earlier version to diff
 * against, so it cannot show what this image documents.
 *
 * The clicks are unconditional: an empty queue must fail the run rather than
 * quietly publish the listing behind it under the review name.
 */
export async function openScriptReview(page: Page): Promise<void> {
  await openQueuedReview(page, "Daily Sales Report");
  const heading = page.getByRole("dialog").getByText(/Code changes since/);
  await heading.scrollIntoViewIfNeeded({ timeout: 3_000 });
  // Scrolling the heading into view stops with the hunk still below the fold,
  // and a capture of a diff that shows no changed line documents nothing.
  await heading.hover();
  await page.mouse.wheel(0, 220);
  await page.waitForTimeout(600);
}

/**
 * openScriptFirstApproval opens the drawer on a script nothing has ever
 * approved, which is the other decision this surface exists for: approving
 * starts something running rather than changing what runs.
 */
export async function openScriptFirstApproval(page: Page): Promise<void> {
  await openQueuedReview(page, "Dormant Accounts");
  await page.waitForTimeout(600);
}

/**
 * openScriptDeliveryGrant opens the drawer on a script that sends data out of
 * the platform and fills in the address the reviewer is agreeing to. It is the
 * sharpest decision the surface carries: a destination the code names has no
 * meaning until a reviewer says which connection, bucket and prefix it resolves
 * to, and until they do, approving is refused.
 */
export async function openScriptDeliveryGrant(page: Page): Promise<void> {
  await openQueuedReview(page, "Dormant Accounts");
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("acme-crm-drop connection").fill("acme-s3");
  await dialog.getByLabel("acme-crm-drop bucket").fill("acme-exports");
  await dialog.getByLabel("acme-crm-drop prefix").fill("retention");
  const heading = dialog.getByText("Output destinations");
  await heading.scrollIntoViewIfNeeded({ timeout: 3_000 });
  // The editor sits below its own label, so stopping at the label leaves the
  // address fields -- the whole subject of this capture -- below the fold.
  await heading.hover();
  await page.mouse.wheel(0, 200);
  await page.waitForTimeout(600);
}

/** openQueuedReview clicks the Review button of one queue row by script name. */
async function openQueuedReview(page: Page, script: string): Promise<void> {
  await page
    .locator("li")
    .filter({ hasText: script })
    .getByRole("button", { name: "Review" })
    .first()
    .click();
  await page.getByRole("dialog").waitFor({ state: "visible", timeout: 5_000 });
}

/**
 * openScriptAlertSettings scrolls the settings page to the managed-script
 * review alert section (#1287), which sits below the SMTP and knowledge-queue
 * sections and so never appears in a viewport capture of the page top.
 */
export async function openScriptAlertSettings(page: Page): Promise<void> {
  await page
    .getByText("Script review queue alert")
    .scrollIntoViewIfNeeded({ timeout: 3_000 });
  await page.waitForTimeout(500);
}

/**
 * openScriptVersionHistory scrolls a script's detail page to its version
 * history, which sits below the contract and the parameters. The served
 * version's source is already open there: what is running right now is the
 * question the section is usually opened with.
 */
export async function openScriptVersionHistory(page: Page): Promise<void> {
  // Scroll to the grant rather than the section heading: the heading is already
  // above the fold on this page, so stopping there captures the contract again
  // under the version-history name and documents nothing new.
  await page
    .getByText("Runs with the authority of")
    .scrollIntoViewIfNeeded({ timeout: 3_000 });
  await page.waitForTimeout(500);
}

/**
 * openScriptRunHistory scrolls to the run history, the refresh record of the
 * automation: a success, the failure that woke somebody, and a fire skipped
 * because the previous run was still going.
 */
export async function openScriptRunHistory(page: Page): Promise<void> {
  await page.getByText("Run history").scrollIntoViewIfNeeded({ timeout: 3_000 });
  await page.waitForTimeout(500);
}

/**
 * openScriptRunLog opens the most recent run in place: its parameters, what it
 * cost, the asset version it produced, and the log it printed while working.
 */
export async function openScriptRunLog(page: Page): Promise<void> {
  await openScriptRunHistory(page);
  await page.getByRole("button", { name: "Open" }).first().click();
  await page.getByText("Computed against").waitFor({ state: "visible", timeout: 5_000 });
  // The log is the point of this capture and sits under the run's facts, so
  // scroll to the log itself rather than to the row that opened it.
  await page.getByText(/wrote asset version/).scrollIntoViewIfNeeded({ timeout: 3_000 });
  await page.waitForTimeout(600);
}
