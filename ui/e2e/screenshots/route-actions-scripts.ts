import { type Page } from "@playwright/test";

// Every capture action for a managed script's own page and listing. They live
// here rather than in route-actions.ts for the reason the refs and tools
// actions do: that module is at its size budget, and one feature's actions are
// read together.

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
  // (#1407): an address nobody has authenticated with cannot open the portal,
  // and a script handed to one would be visible to administrators alone. The
  // capture is of the confirmation that follows the choice, because that is
  // where the transfer states what it does with the files the script's runs
  // have written (#1588): how many there are, and the box that moves them.
  // Nothing is confirmed; the mock server keeps the script for the captures
  // that follow.
  await page
    .getByLabel("New owner")
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page
    .getByRole("option")
    .first()
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page
    .getByRole("button", { name: "Transfer ownership" })
    .click({ timeout: 3_000 })
    .catch(() => {});
  await page.waitForTimeout(600);
}

/**
 * openScriptDelete opens the confirmation behind the script's delete control
 * (#1575). The capture is of the confirmation rather than of the button,
 * because the list it states -- the saved versions, the schedule, the run
 * history and the carried state that go, and the files that do not -- is the
 * whole of what the control is for.
 *
 * Nothing is confirmed. The dialog is opened and photographed; the mock server
 * these captures share keeps the script for the ones that follow.
 */
export async function openScriptDelete(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Delete script" }).click({ timeout: 3_000 });
  // Waited on rather than timed out: a swallowed click would publish the page
  // with no dialog on it, captioned as the confirmation.
  await page.getByRole("dialog").waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(400);
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
