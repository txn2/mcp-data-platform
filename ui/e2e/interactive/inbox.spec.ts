import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The Feedback section is the Inbox (#1798): one place for everything
// addressed to the reader, threads and notifications together.
//
// It was also the one portal page pinned to the viewport. AppShell scrolls the
// whole page in `main`; this page opened a `h-full` column and gave each tab
// panel its own `overflow-auto`, so three threads filled the screen and the tab
// strip never scrolled away. The Worklist went a level deeper -- a second tab
// strip under the first, with a second scroll region inside the first.

const INBOX = "/portal/feedback";
const SETTINGS = "/portal/settings";

async function openInbox(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto(INBOX);
  await expect(page.getByRole("tab", { name: /Recent/ })).toBeVisible({ timeout: 20_000 });
}

/** Every scrollable element inside the page's main region. */
async function innerScrollers(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const main = document.querySelector("main");
    if (!main) return ["no main"];
    const bad: string[] = [];
    for (const el of main.querySelectorAll("*")) {
      const style = getComputedStyle(el);
      const scrolls = ["auto", "scroll"].includes(style.overflowY);
      if (!scrolls) continue;
      // A box that scrolls only because its content happens to exceed it is
      // still a second scroll region the reader has to find.
      if (el.scrollHeight > el.clientHeight + 1) {
        bad.push(`${el.tagName.toLowerCase()}.${el.className}`.slice(0, 120));
      }
    }
    return bad;
  });
}

test.describe("The section is the Inbox", () => {
  test("the sidebar and the header both name it", async ({ page }) => {
    await openInbox(page);
    await expect(page.locator("nav").getByText("Inbox", { exact: true })).toBeVisible();
    await expect(page.getByRole("banner").getByText("Inbox")).toBeVisible();
  });

  test("no portal surface still calls it Feedback", async ({ page }) => {
    await openInbox(page);
    await expect(page.locator("nav").getByText("Feedback", { exact: true })).toHaveCount(0);
    await expect(page.getByRole("banner").getByText("Feedback", { exact: true })).toHaveCount(0);
  });
});

test.describe("The notifications the platform sent are in it", () => {
  test("the Notifications view lists them, newest first, with status and time", async ({ page }) => {
    await openInbox(page);
    await page.getByRole("tab", { name: /Notifications/ }).click();

    // The retention sentence sits above the list, as it did on settings.
    await expect(page.getByText(/What the platform has sent you/)).toBeVisible();

    const rows = page.locator("main li");
    await expect(rows.first()).toBeVisible();

    // One for one with what the endpoint returns for this caller.
    const served = await page.evaluate(async () => {
      const res = await fetch("/api/v1/portal/notifications?page=1&per_page=20");
      return (await res.json()) as { data: { subject: string }[] };
    });
    expect(served.data.length).toBeGreaterThan(0);
    await expect(rows).toHaveCount(served.data.length);
    await expect(rows.first()).toContainText(served.data[0]!.subject);
  });

  test("settings no longer carries the history, and says where it went", async ({ page }) => {
    await authenticate(page);
    await page.goto(SETTINGS);
    await expect(page.getByText("Recent notifications")).toHaveCount(0);
    // The preferences stay -- they are a setting.
    await expect(page.getByText(/Email notifications for sharing/)).toBeVisible();
    await expect(page.getByText(/listed in the Inbox/)).toBeVisible();
  });
});

test.describe("It scrolls like the rest of the portal", () => {
  for (const tab of ["Recent", "Worklist", "General", "Notifications"]) {
    test(`the ${tab} view opens no scroll region of its own`, async ({ page }) => {
      await openInbox(page);
      await page.getByRole("tab", { name: new RegExp(tab) }).click();
      // Let the list settle before measuring.
      await page.waitForTimeout(400);
      expect(await innerScrollers(page)).toEqual([]);
    });
  }

  test("the Worklist presents its lists without a second row of tabs", async ({ page }) => {
    await openInbox(page);
    await page.getByRole("tab", { name: /Worklist/ }).click();

    // One tab strip on the page: the Inbox's own.
    await expect(page.getByRole("tablist")).toHaveCount(1);

    // And the three lists are still reachable, with their counts.
    for (const label of ["Needs resolution", "Awaiting my validation", "Mentions of me"]) {
      await expect(page.getByRole("button", { name: new RegExp(label) })).toBeVisible();
    }
  });

  test("a chip states which list is shown", async ({ page }) => {
    await openInbox(page);
    await page.getByRole("tab", { name: /Worklist/ }).click();
    const mentions = page.getByRole("button", { name: /Mentions of me/ });
    await mentions.click();
    await expect(mentions).toHaveAttribute("aria-pressed", "true");
  });
});
