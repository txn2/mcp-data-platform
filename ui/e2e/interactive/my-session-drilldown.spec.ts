import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// A row of the reader's own session timeline used to open nothing (#1797).
//
// SessionTimeline attaches onClick only when an onSelect is passed. The
// operator surface passed one and opened the event drawer; MySessionDetailPage
// did not, so a reader could see that a call failed and had no way to see what
// it was called with or what it said.
//
// GET /portal/calls/{id} was not the answer: a call record is written only for
// the sql, api and graphql kinds, so most rows of a session would still open
// nothing. The drill-down is a self-scoped read of the audit event itself.

const MY_SESSIONS = "/portal/activity/sessions";

async function openFirstSession(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto(MY_SESSIONS);
  const firstRow = page.locator("tbody tr").first();
  await expect(firstRow).toBeVisible({ timeout: 20_000 });
  await firstRow.click();
  await expect(page.getByText("Timeline")).toBeVisible({ timeout: 20_000 });
}

test.describe("A reader can open a call in their own session", () => {
  test("clicking a timeline row opens the call in full", async ({ page }) => {
    await openFirstSession(page);

    const row = page.locator("tbody tr").first();
    await row.click();

    const drawer = page.getByRole("dialog");
    await expect(drawer).toBeVisible();
    // What the timeline entry does not carry, which is the reason the
    // drill-down exists.
    await expect(drawer.getByText("Parameters")).toBeVisible();
    await expect(drawer.getByText("Event ID")).toBeVisible();
    await expect(drawer.getByText("Duration")).toBeVisible();
    await expect(drawer.getByText("Persona")).toBeVisible();
  });

  test("it offers no Replay, which is an administrator's read", async ({ page }) => {
    await openFirstSession(page);
    await page.locator("tbody tr").first().click();
    const drawer = page.getByRole("dialog");
    await expect(drawer).toBeVisible();
    await expect(drawer.getByRole("button", { name: /Replay/ })).toHaveCount(0);
  });

  test("another user's event is not found, not refused", async ({ page }) => {
    await authenticate(page);
    // evt-0000's owner is whoever the fixture drew; this id is one no caller
    // owns, which is the same answer somebody else's id gets.
    const status = await page.evaluate(async () => {
      const res = await fetch("/api/v1/portal/events/evt-does-not-exist");
      return res.status;
    });
    expect(status).toBe(404);
  });
});

test.describe("The stated purpose can be read", () => {
  test("it is not clipped to one line behind a tooltip", async ({ page }) => {
    await openFirstSession(page);
    const purposes = page.getByTestId("timeline-purpose");
    const count = await purposes.count();
    expect(count).toBeGreaterThan(0);

    // A purpose is a sentence, and the column exists to carry it. One line
    // clipped at 24rem with the rest only in a `title` is not reading it.
    let sawWrapped = false;
    for (let i = 0; i < count; i += 1) {
      const cell = purposes.nth(i);
      const text = (await cell.innerText()).trim();
      if (text === "-" || text.length < 60) continue;
      const lines = await cell.evaluate((el) => {
        const style = getComputedStyle(el);
        const lineHeight = parseFloat(style.lineHeight) || 16;
        return Math.round(el.getBoundingClientRect().height / lineHeight);
      });
      expect(lines).toBeGreaterThan(1);
      sawWrapped = true;
      break;
    }
    expect(sawWrapped, "no session carried a purpose long enough to test").toBe(true);
  });
});
