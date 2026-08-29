import { type Page } from "@playwright/test";
import { openResourceNamed } from "./route-actions-library";

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
 * openAssetProvenance opens the asset viewer's metadata sidebar, which is where
 * the provenance panel lives: the calls the asset was built from, grouped by
 * the write that captured them (#1320).
 */
export async function openAssetProvenance(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Show details" }).click();
  await page.getByText("Provenance").first().waitFor();
  await page.waitForTimeout(400);
}

/**
 * openAssetMetadataEdit opens the asset viewer's metadata sidebar and puts it in
 * edit mode, which is the only state showing the retention control: how much
 * version history this asset keeps (#1421).
 */
export async function openAssetMetadataEdit(page: Page): Promise<void> {
  await openAssetProvenance(page);
  await page.getByRole("button", { name: "Edit" }).first().click();
  await page.getByText("Version history").waitFor();
  await page.waitForTimeout(400);
}

/**
 * openAssetProvenanceEarlier opens the disclosure every capture but the newest
 * sits behind, and then one of those captures. It is the only state showing an
 * earlier write's calls, which the panel no longer renders all at once (#1422).
 */
export async function openAssetProvenanceEarlier(page: Page): Promise<void> {
  await openAssetProvenance(page);
  await page.getByRole("button", { name: /earlier capture/ }).click();
  await page.getByRole("button", { name: /Version \d/ }).first().click();
  await page.waitForTimeout(400);
}

/**
 * openAssetProvenanceCall opens one captured call from the provenance panel,
 * which is the only state showing the statement, the stated purpose, the
 * outcome, and the mcp:call: reference an agent cites (#1320). The call it
 * opens belongs to an earlier capture, so it walks through the disclosure.
 */
export async function openAssetProvenanceCall(page: Page): Promise<void> {
  await openAssetProvenanceEarlier(page);
  await page.getByRole("button", { name: /Trino Query/ }).first().click();
  await page.getByRole("dialog").waitFor();
  await page.waitForTimeout(400);
}

/**
 * openAssetVersionPicker opens the asset viewer's version list, the only state
 * showing when each version was written. A scheduled script refreshes an asset
 * hourly, so the version number alone does not identify one (#1422).
 */
export async function openAssetVersionPicker(page: Page): Promise<void> {
  await page.getByRole("combobox", { name: "Asset version" }).click();
  await page.getByRole("listbox").waitFor();
  await page.waitForTimeout(400);
}

/**
 * openShareDialogPublicLink opens the Share dialog and switches the link to
 * "Anyone with the link", which is the only state showing the anonymous-access
 * warning and the lifetime control: a public link is the one share that
 * expires on a clock (#1279).
 */
export async function openShareDialogPublicLink(page: Page): Promise<void> {
  await openShareDialog(page);
  await page.getByLabel("Who can open this link").click();
  await page.getByRole("option", { name: "Anyone with the link" }).click();
  await page.getByText(/works without signing in/).waitFor();
  await page.waitForTimeout(400);
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
  await openResourceNamed(page, "SQL Style Guide");
}

/**
 * openResourceLifecycle opens the same page and scrolls its sidebar to the
 * lifecycle panels. Those sit below the fold of that column, so a capture of
 * the page as it opens shows the identity and the content and cannot reach the
 * usage rollup, the version trail, or the prompts attaching the resource --
 * which is what the docs prose beside this image documents.
 */
export async function openResourceLifecycle(page: Page): Promise<void> {
  await openResourceDetail(page);
  // The end of the column rather than the usage rollup: the rollup is already
  // on screen when the page opens, so scrolling to it would produce a second
  // capture identical to the first.
  await page
    .getByTestId("resource-used-by-prompts")
    .scrollIntoViewIfNeeded({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(500);
}

/**
 * openResourceMove opens the edit dialog on the resource detail page and picks a
 * library to move the file into (#1502).
 *
 * The picked library matters: the destination note only appears once the
 * selection differs from where the file is, and that note -- who will be able to
 * see the file, and that its address changes while the address already written
 * down keeps resolving -- is the whole point of the capture.
 */
export async function openResourceMove(page: Page): Promise<void> {
  // A resource in one person's own library, rather than the revision-trail
  // fixture the other captures open: the file this documents is one whose
  // library is about to widen, and the global fixture is already where the
  // widest move would land.
  await openResourceNamed(page, "Query Templates");
  await page.getByRole("button", { name: "Edit" }).first().click({ timeout: 3_000 });
  await page.getByRole("combobox", { name: "Library" }).click({ timeout: 3_000 });
  await page.getByRole("option", { name: "Global" }).click({ timeout: 3_000 });
  // Waited on rather than timed out: the note appears only once the selection
  // differs from where the file is, so a swallowed click would publish the
  // dialog captioned as a move with no move selected in it.
  await page.getByTestId("library-move-note").waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(400);
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
 * openScriptVersionHistory opens the version history, which is folded into the
 * Source section behind a reveal (#1406), and expands one version in it. The
 * editor above already holds the version that runs, so what this documents is
 * the versions before it and the roles each of them runs under.
 */
export async function openScriptVersionHistory(page: Page): Promise<void> {
  await page.getByRole("button", { name: /Version history/ }).click({ timeout: 3_000 });
  // The OLDEST version in the list, not the newest: the newest is the text in
  // the editor directly above, and a capture of the same source twice on one
  // page documents nothing.
  await page.getByText(/^v\d+$/).last().click({ timeout: 3_000 });
  // Scroll to the authority line rather than the reveal: the reveal is what
  // was clicked, and the source and the roles under it are what the capture is
  // for.
  await page
    .getByText("A run of this version presents")
    .first()
    .scrollIntoViewIfNeeded({ timeout: 3_000 });
  await page.waitForTimeout(500);
}

/**
 * openScriptSource scrolls to the code, which is the section an owner edits,
 * runs, and dry-runs (#1406). The parameters one form binds for both runs are
 * filled first, so the capture frames the controls in the state somebody
 * actually presses them in rather than greyed out.
 */
export async function openScriptSource(page: Page): Promise<void> {
  await bindRunParameters(page);
  await page
    .getByText("Saving makes this the version that runs")
    .scrollIntoViewIfNeeded({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(600);
}

/**
 * openScriptDocumentation opens the form an owner documents a script in
 * (#1369): the display name, the markdown description, the category the script
 * is filed under, and its tags. The read state of the same section is on the
 * detail capture, where the description is already rendered as the document it
 * is; what this one adds is the surface it is written on.
 */
export async function openScriptDocumentation(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Edit" }).first().click();
  await page.getByLabel("Script description").waitFor({ timeout: 3_000 });
  await page.waitForTimeout(600);
}

/**
 * bindRunParameters supplies the fixture script's two required values on the
 * Source section's parameter form, which is the one form a run and a dry run
 * both bind (#1406). Its control ids are scoped to that form, which is what
 * lets a capture drive it without touching the schedule's bindings below it.
 *
 * The form is filled the way a person fills it, so a capture shows the control
 * a connection parameter actually is: a choice from the set this script may
 * reach, rather than a box somebody has to spell a name into.
 */
async function bindRunParameters(page: Page): Promise<void> {
  await page
    .locator("#script-param-run-report_date")
    .fill("2026-08-17", { timeout: 3_000 })
    .catch(() => {});
  await page
    .locator("#script-param-run-source")
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page
    .getByRole("option", { name: /acme-warehouse/ })
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(300);
}

/**
 * openScriptDryRun executes the saved source as the caller and frames what came
 * back (#1364): the metrics, the shape of the outputs nothing wrote, and the
 * log, which is the whole reason to have run it.
 */
export async function openScriptDryRun(page: Page): Promise<void> {
  // A dry run binds the same values a real one does, so the required ones are
  // supplied first: the control is deliberately unavailable until they are.
  await bindRunParameters(page);
  await page
    .getByRole("button", { name: "Dry run" })
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(900);
  await page
    .getByText("Nothing was persisted", { exact: false })
    .scrollIntoViewIfNeeded({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(400);
}

/**
 * openScriptRunsTab switches the admin script surface to the operator's view
 * of what has been running: the metric panels and the cross-script history.
 */
export async function openScriptRunsTab(page: Page): Promise<void> {
  await page
    .getByRole("tab", { name: "Runs" })
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(900);
}

/**
 * openScriptSchedule scrolls to the schedule controls: how often it repeats,
 * the zone the time is read in, the binding every fire passes, and the pause
 * that retires it.
 */
export async function openScriptSchedule(page: Page): Promise<void> {
  // The section is folded by default (#1407): its header states what the
  // script runs, and the builder is behind the reveal this opens.
  await page
    .getByRole("button", { name: /^Schedule/ })
    .click({ timeout: 3_000 })
    .catch(() => {});
  // The builder's first control, which is where the schedule is chosen — the
  // page no longer has a cron field to scroll to (#1307).
  await page
    .getByRole("group", { name: "How often this script runs" })
    .scrollIntoViewIfNeeded({ timeout: 3_000 });
  await page.waitForTimeout(500);
}

/**
 * openScriptState opens the State section (#1537): the one JSON object the
 * script carries from run to run, folded by default with its revision in the
 * header, and the owner's two resets beneath the object once it is open.
 */
export async function openScriptState(page: Page): Promise<void> {
  await page
    .getByRole("button", { name: /^State/ })
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.getByTestId("script-state").scrollIntoViewIfNeeded({ timeout: 3_000 });
  await page.waitForTimeout(500);
}

/**
 * openScriptOwner scrolls to the owner section, the one control on a script's
 * page that belongs to an administrator rather than to its owner (#1404):
 * moving the script to another person, which hands over everything the owner
 * has at once.
 */
export async function openScriptOwner(page: Page): Promise<void> {
  await page.getByText("Transfer ownership").scrollIntoViewIfNeeded({ timeout: 3_000 });
  // The new owner is CHOSEN, from the people who have actually signed in
  // (#1407), so the capture is of the choice rather than of a closed control:
  // an address nobody has authenticated with cannot open the portal, and a
  // script handed to one would be visible to administrators alone.
  await page
    .getByLabel("New owner")
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(600);
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
  await page.getByRole("row").filter({ hasText: "succeeded" }).first().click();
  await page.getByText("Computed against").waitFor({ state: "visible", timeout: 5_000 });
  // The log is the point of this capture and sits under the run's facts, so
  // scroll to the log itself rather than to the row that opened it.
  await page.getByText(/wrote asset version/).scrollIntoViewIfNeeded({ timeout: 3_000 });
  await page.waitForTimeout(600);
}

/**
 * openCollectionDetailsDialog opens the admin collection viewer's Edit details
 * form, the only state showing the two fields an admin may correct on a
 * collection owned by someone else (#1292).
 */
export async function openCollectionDetailsDialog(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Edit details" }).click({ timeout: 3_000 });
  await page.getByLabel("Name").waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(400);
}

/**
 * showTablesPanel scrolls the "Query as a table" section into view. It sits
 * below the preview in the resource dialog and below the provenance panel in
 * the asset viewer's sidebar, so a capture of either as it opens cannot reach
 * it (#1327).
 */
export async function showTablesPanel(page: Page): Promise<void> {
  await page
    .getByText("Query as a table", { exact: true })
    .first()
    .scrollIntoViewIfNeeded({ timeout: 5_000 });
  // Scrolling the heading into view leaves the registration itself below the
  // fold, and the registration is the substance of the capture.
  const registration = page.locator('[data-testid^="table-registration-"]').first();
  if (await registration.isVisible().catch(() => false)) {
    await registration.scrollIntoViewIfNeeded({ timeout: 3_000 }).catch(() => {});
  }
  await page.waitForTimeout(500);
}

/**
 * openAssetTables opens the asset viewer's metadata sidebar and scrolls to the
 * registered-table panel. The sidebar is closed until "Show details" is
 * pressed, so the panel is not merely below the fold -- it is not mounted.
 */
export async function openAssetTables(page: Page): Promise<void> {
  await openAssetProvenance(page);
  await showTablesPanel(page);
}

/**
 * openGlossaryResourceTables opens the CSV resource carrying a stale
 * registration and scrolls to it: the file has a newer revision than the table
 * points at, which is the one state a reader cannot discover from the rows.
 */
export async function openGlossaryResourceTables(page: Page): Promise<void> {
  await openResourceNamed(page, "Business Glossary Export");
  await showTablesPanel(page);
}

/**
 * openTableRegisterForm opens the register form on that same page: which
 * connection the table is created on, and what it is called.
 */
export async function openTableRegisterForm(page: Page): Promise<void> {
  await openGlossaryResourceTables(page);
  await page.getByRole("button", { name: "Register", exact: true }).first().click({ timeout: 3_000 });
  await page.getByLabel("Connection").waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(400);
}

/**
 * openStoreListResourceTables opens the CSV resource whose cells carry line
 * breaks -- a multi-line store address in one cell -- and scrolls to its
 * registered-table panel (#1441).
 */
export async function openStoreListResourceTables(page: Page): Promise<void> {
  await openResourceNamed(page, "Store List");
  await showTablesPanel(page);
}

/**
 * openTableRepairOffer submits the register form over that file and stops on
 * the refusal: what is wrong with the file, and the control that corrects it.
 * It is the state a person meets when their spreadsheet export cannot be read
 * as a table the way it is stored (#1441).
 */
export async function openTableRepairOffer(page: Page): Promise<void> {
  await openStoreListResourceTables(page);
  await page.getByRole("button", { name: "Register", exact: true }).first().click({ timeout: 3_000 });
  await page.getByLabel("Connection").waitFor({ state: "visible", timeout: 5_000 });
  await page.getByRole("button", { name: "Register", exact: true }).last().click({ timeout: 3_000 });
  await page.getByTestId("table-repair-button").waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(400);
}

/**
 * openTableRepaired takes the offer and stops on what the correction changed:
 * the file has a new version, which outlives the registration that caused it.
 */
export async function openTableRepaired(page: Page): Promise<void> {
  await openTableRepairOffer(page);
  await page.getByTestId("table-repair-button").click({ timeout: 3_000 });
  await page.getByTestId("table-repair-notice").waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(400);
}

/**
 * openCorrectedVersion scrolls the same page to the version history, where the
 * correction is recorded: the new version carries the description of what
 * changed, and the version below it -- the bytes its owner uploaded -- has none
 * (#1450).
 */
export async function openCorrectedVersion(page: Page): Promise<void> {
  await openTableRepaired(page);
  await page.getByTestId("resource-versions").scrollIntoViewIfNeeded({ timeout: 3_000 });
  // Waited on rather than timed out: a swallowed scroll would ship an
  // un-scrolled page captioned as the version history, and a panel that
  // rendered without the summary is the defect this capture documents.
  await page
    .getByTestId("resource-version-summary-2")
    .waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(300);
}
