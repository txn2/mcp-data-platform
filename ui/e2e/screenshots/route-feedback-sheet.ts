import { type Page } from "@playwright/test";
import { openFeedbackMentionComposer } from "./route-actions";
import { type ScreenshotRoute } from "./route-types";

/**
 * An asset's feedback on a phone (#2705), one capture per state: the thread
 * list in the sheet, a thread with the reply box folded to one row, the sheet
 * made full size, replying, new feedback, and the small phone's thread.
 *
 * The run shares one page at 1440x900, so each capture sets the phone's size
 * and puts the desktop size and the default sheet back afterwards.
 */

const ASSET = "/portal/assets/ast-001";
const SHEET_SIZE_KEY = "feedback-sheet-size";
const PHONE = { width: 390, height: 844 };
const SMALL_PHONE = { width: 375, height: 667 };

async function openSheet(page: Page, size: { width: number; height: number }): Promise<void> {
  await page.setViewportSize(size);
  await page.waitForTimeout(400);
  await page.getByRole("main").getByRole("button", { name: /Feedback/ }).first().click();
  await page.locator("[data-item-panel]").getByRole("button").first().waitFor();
  await page.waitForTimeout(300);
}

async function openThread(page: Page, size = PHONE): Promise<void> {
  await openSheet(page, size);
  await page.locator('[data-testid="feedback-panel-body"] button').first().click();
  await page.getByRole("button", { name: "Back to list" }).waitFor();
  await page.waitForTimeout(400);
}

async function restore(page: Page): Promise<void> {
  await page.evaluate((key) => {
    try {
      localStorage.removeItem(key);
      // A reply draft opens the reply box at once, so the next capture would
      // find no "Reply…" row to tap.
      for (const k of Object.keys(sessionStorage)) {
        if (k.startsWith("thread-reply-draft:")) sessionStorage.removeItem(k);
      }
    } catch {
      // Nothing kept.
    }
  }, SHEET_SIZE_KEY);
  await page.setViewportSize({ width: 1440, height: 900 });
}

export const assetFeedbackRoutes: ScreenshotRoute[] = [
  {
    // Mention composer (#627): the audience-scoped type-ahead open over the
    // new-feedback form.
    slug: "asset-feedback-mention",
    path: ASSET,
    category: "user",
    beforeCapture: openFeedbackMentionComposer,
  },
  {
    slug: "asset-feedback-sheet-list",
    path: ASSET,
    category: "user",
    beforeCapture: (page) => openSheet(page, PHONE),
    afterCapture: restore,
  },
  {
    slug: "asset-feedback-sheet-thread",
    path: ASSET,
    category: "user",
    beforeCapture: (page) => openThread(page),
    afterCapture: restore,
  },
  {
    slug: "asset-feedback-sheet-full",
    path: ASSET,
    category: "user",
    beforeCapture: async (page) => {
      await openThread(page);
      await page.getByTestId("sheet-handle").click();
      await page.waitForTimeout(300);
    },
    afterCapture: restore,
  },
  {
    slug: "asset-feedback-sheet-replying",
    path: ASSET,
    category: "user",
    beforeCapture: async (page) => {
      await openThread(page);
      await page.getByRole("button", { name: "Reply…" }).click();
      await page.getByLabel("Reply", { exact: true }).fill("Thanks. Section 2 is updated too.");
      await page.waitForTimeout(400);
    },
    afterCapture: restore,
  },
  {
    slug: "asset-feedback-sheet-more",
    path: ASSET,
    category: "user",
    beforeCapture: async (page) => {
      await openThread(page);
      await page.getByRole("button", { name: "More for this thread" }).click();
      await page.waitForTimeout(300);
    },
    afterCapture: async (page) => {
      await page.keyboard.press("Escape");
      await restore(page);
    },
  },
  {
    slug: "asset-feedback-sheet-new",
    path: ASSET,
    category: "user",
    beforeCapture: async (page) => {
      await openSheet(page, PHONE);
      await page.getByRole("button", { name: "New", exact: true }).click();
      await page.waitForTimeout(300);
    },
    afterCapture: restore,
  },
  {
    slug: "asset-feedback-sheet-small-thread",
    path: ASSET,
    category: "user",
    beforeCapture: (page) => openThread(page, SMALL_PHONE),
    afterCapture: restore,
  },
];
