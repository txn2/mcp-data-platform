import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the managed-script review surface (#1287). The unit
// tests drive the page with its hooks mocked; these drive the assembled app
// against the mock server, so the route, the query keys, and the two decisions
// are exercised end to end -- including the part a mocked hook cannot show:
// that a decision actually clears the row from the queue it was made in.

async function gotoScripts(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/admin/scripts");
  await expect(page.getByText("Awaiting approval")).toBeVisible();
}

async function openReview(page: Page, script: string): Promise<void> {
  // The queue item is itself the control that opens the review, named for what
  // it opens, as every other row in the portal is.
  await page
    .getByRole("button", { name: new RegExp(`^Review ${script}`) })
    .first()
    .click();
  await expect(page.getByRole("dialog")).toBeVisible();
}

test.describe("Script review queue", () => {
  // The operator's other question: not what needs a decision, but what has
  // been running (#1307).
  test("the Runs tab shows what the platform has been running", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("tab", { name: "Runs" }).click();

    // The charts read the metrics the run worker emits; the table reads the
    // run rows. Both are on the page, and neither can do the other's job.
    await expect(page.getByText("Runs over time")).toBeVisible();
    await expect(page.getByText("Missed fires")).toBeVisible();
    await expect(page.getByText("Recent runs")).toBeVisible();
    await expect(page.getByRole("row").filter({ hasText: "succeeded" }).first()).toBeVisible();
  });

  test("lists what is not executing, and says which kind of decision each row is", async ({
    page,
  }) => {
    await gotoScripts(page);

    await expect(page.getByText("First approval")).toBeVisible();
    await expect(page.getByText("Change to a running script")).toBeVisible();
    // The listing states what each script is executing today.
    await expect(page.getByText("Nothing approved")).toBeVisible();
    await expect(page.getByText("Approved v2")).toBeVisible();
  });

  test("a change to a running script shows both diffs against what runs today", async ({
    page,
  }) => {
    await gotoScripts(page);
    await openReview(page, "Daily Sales Report");
    const dialog = page.getByRole("dialog");

    await expect(dialog.getByText("Running v2 today")).toBeVisible();
    await expect(dialog.getByText("Widens authority")).toBeVisible();
    // The connection the new code reaches for is marked as new authority.
    await expect(dialog.getByText("+ acme-finance")).toBeVisible();
    // And the code change is the diff, not the whole file.
    await expect(dialog.getByText("Code changes since v2")).toBeVisible();
    await expect(dialog.getByText(/\+margins = platform\.query\(/)).toBeVisible();
  });

  test("a first approval has no baseline, so the whole source is the change", async ({
    page,
  }) => {
    await gotoScripts(page);
    await openReview(page, "Dormant Accounts");
    const dialog = page.getByRole("dialog");

    await expect(dialog.getByText("Nothing of this script runs yet")).toBeVisible();
    await expect(dialog.getByText(/This script executes nothing today/).first()).toBeVisible();
    await expect(dialog.getByText(/dormant accounts:/)).toBeVisible();
  });

  test("a script that sends data out of the platform cannot be approved until the reviewer says where", async ({
    page,
  }) => {
    await gotoScripts(page);
    await openReview(page, "Dormant Accounts");
    const dialog = page.getByRole("dialog");

    // The code names a destination; no approval has ever given it an address,
    // so the decision is incomplete rather than absent.
    await expect(dialog.getByText("acme-crm-drop", { exact: true })).toBeVisible();
    await expect(dialog.getByText("Needs an address")).toBeVisible();
    await expect(
      dialog.getByRole("button", { name: /Approve and bind this grant/ }),
    ).toBeDisabled();
    await expect(dialog.getByText(/Say where acme-crm-drop writes/)).toBeVisible();

    await dialog.getByLabel("acme-crm-drop connection").fill("acme-s3");
    await dialog.getByLabel("acme-crm-drop bucket").fill("acme-exports");
    await dialog.getByLabel("acme-crm-drop prefix").fill("retention");

    // With an address, the destination reads as the place it is, and the
    // decision is available.
    await expect(
      dialog.getByText("acme-crm-drop -> s3 acme-s3 acme-exports/retention"),
    ).toBeVisible();
    await expect(
      dialog.getByRole("button", { name: /Approve and bind this grant/ }),
    ).toBeEnabled();

    await dialog.getByRole("button", { name: /Approve and bind this grant/ }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByText("First approval")).toHaveCount(0);
  });

  test("the authority being bound is stated and cannot be edited", async ({ page }) => {
    await gotoScripts(page);
    await openReview(page, "Daily Sales Report");
    const dialog = page.getByRole("dialog");

    await expect(dialog.getByText("Authority this version would run with")).toBeVisible();
    await expect(dialog.getByText("analyst", { exact: true })).toBeVisible();
    await expect(dialog.getByText(/Approving cannot change it/)).toBeVisible();
  });

  test("approving clears the row and points the script at the approved version", async ({
    page,
  }) => {
    await gotoScripts(page);
    await openReview(page, "Daily Sales Report");

    await page.getByRole("button", { name: /Approve and bind this grant/ }).click();

    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByText("Change to a running script")).toHaveCount(0);
    await expect(page.getByText("Approved v3")).toBeVisible();
  });

  test("an approval the code would outrun is refused, in place, saying what is missing", async ({
    page,
  }) => {
    await gotoScripts(page);
    await openReview(page, "Daily Sales Report");
    const dialog = page.getByRole("dialog");

    // Take away both host functions the code calls, then approve.
    await dialog.getByRole("button", { name: /platform\.query/ }).click();
    await dialog.getByRole("button", { name: /platform\.export/ }).click();
    await dialog.getByRole("button", { name: /Approve and bind this grant/ }).click();

    await expect(dialog.getByText(/does not cover capabilities/)).toBeVisible();
    // The drawer stays open on the refusal: the reviewer fixes the grant here.
    await expect(page.getByRole("dialog")).toBeVisible();
  });

  test("rejecting a draft clears the row and changes nothing about what runs", async ({
    page,
  }) => {
    await gotoScripts(page);
    await openReview(page, "Daily Sales Report");

    await page.getByRole("button", { name: "Reject" }).click();

    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByText("Change to a running script")).toHaveCount(0);
    // The approved version is untouched: the script still runs v2.
    await expect(page.getByText("Approved v2")).toBeVisible();
  });

  test("a version that is not a pending draft offers no rejection", async ({ page }) => {
    await gotoScripts(page);
    await openReview(page, "Dormant Accounts");
    const dialog = page.getByRole("dialog");

    await expect(dialog.getByRole("button", { name: "Reject" })).toHaveCount(0);
    await expect(dialog.getByText(/nothing to reject here/)).toBeVisible();
  });

  // A row opens the script itself, on the same page its owner opens (#1367).
  // The administrator's version of it adds the decision and takes nothing away,
  // which is the whole claim this test exists to hold.
  test("a row opens the script, where an administrator does everything an owner does", async ({
    page,
  }) => {
    await gotoScripts(page);

    await page.getByRole("row").filter({ hasText: "daily-sales-report" }).click();
    await expect(page).toHaveURL(/\/admin\/scripts\/script-001$/);
    // The shell names the page for what it is showing, which a detail route
    // under a section it does not know would otherwise get wrong.
    await expect(page.getByRole("heading", { name: "Script", level: 1 })).toBeVisible();

    // Everything an owner has: run it, edit it, check the edit, re-time it.
    await expect(page.getByRole("button", { name: "Run", exact: true })).toBeVisible();
    await expect(page.getByText("Source", { exact: true })).toBeVisible();
    await expect(page.getByText("Version history")).toBeVisible();
    await expect(page.getByText("Run history")).toBeVisible();

    // Plus the decision. The version executing today opens by default, so its
    // grant is the one in front of the reviewer; approving an earlier version
    // from here is the rollback path.
    await page.getByRole("button", { name: /Review the grant/ }).click();
    await expect(page.getByRole("dialog")).toBeVisible();
  });

  // The other half of #1367: a personal script its own owner wrote never
  // reaches this queue, and the surfaces say plainly that nobody reviewed it.
  test("a personal script the owner wrote is approved with nobody in the queue", async ({
    page,
  }) => {
    await gotoScripts(page);

    const queue = page.getByRole("button", { name: /^Review My Margin Check/ });
    await expect(queue).toHaveCount(0);

    await page.getByRole("row").filter({ hasText: "my-margin-check" }).click();
    await expect(page.getByText(/v1 automatically on .* nobody reviewed it/)).toBeVisible();
    // Exact, because the contract line above says the same thing in a sentence.
    await expect(page.getByText("Nobody reviewed it", { exact: true })).toBeVisible();
  });
});
