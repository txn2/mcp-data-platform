import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";
import { applyTheme } from "../screenshots/helpers/theme";
import { portalNavItems } from "../../src/components/layout/sidebar/navItems";
import { SECTION_INTROS, sectionIntroFor } from "../../src/components/patterns/sectionIntros";

// Interactive coverage for the section intros (#1570): what a reader is told
// each portal section is for, whether the telling stops when they close it, and
// whether it costs them the controls they came for.
//
// A fresh browser context per test is a reader who has never opened the
// section, which is the state criterion 2 is about.

const PORTAL = "/portal";

/** The intro's disclosure button, whichever state it is in. */
function toggle(page: Page) {
  return page.getByRole("button", { name: /this section is for/i });
}

async function open(page: Page, path: string): Promise<void> {
  await authenticate(page);
  await page.goto(`${PORTAL}${path === "/" ? "/" : path}`);
  await expect(page.getByTestId("section-intro")).toBeVisible();
}

test.describe("Section intros", () => {
  for (const intro of SECTION_INTROS) {
    const label = portalNavItems.find((i) => i.path === intro.path)?.label ?? intro.path;

    test(`${label} states what it holds and what it does not`, async ({ page }) => {
      await open(page, intro.path);

      const card = page.getByTestId("section-intro");
      await expect(card).toContainText(intro.belongs);
      await expect(card).toContainText(intro.notHere);
      await expect(page.getByTestId("section-intro-detail")).toBeVisible();
      await expect(toggle(page)).toHaveAttribute("aria-expanded", "true");
    });
  }

  test("Assets and Resources each send the other kind of file to the other section", async ({
    page,
  }) => {
    await open(page, "/");
    await expect(page.getByTestId("section-intro")).toContainText(
      sectionIntroFor("/")!.notHere,
    );
    await page.goto(`${PORTAL}/resources`);
    await expect(page.getByTestId("section-intro")).toContainText(
      sectionIntroFor("/resources")!.notHere,
    );
  });

  test("a section the reader closes stays closed, and keeps its one-line summary", async ({
    page,
  }) => {
    await open(page, "/resources");
    const intro = sectionIntroFor("/resources")!;

    await toggle(page).click();
    await expect(toggle(page)).toHaveAttribute("aria-expanded", "false");
    await expect(page.getByTestId("section-intro")).toContainText(intro.summary);
    await expect(page.getByTestId("section-intro-detail")).toBeHidden();

    await page.reload();
    await expect(toggle(page)).toHaveAttribute("aria-expanded", "false");
    await expect(page.getByTestId("section-intro")).toContainText(intro.summary);

    // Closing one section says nothing about the others.
    await page.goto(`${PORTAL}/prompts`);
    await expect(toggle(page)).toHaveAttribute("aria-expanded", "true");
  });

  test("the Knowledge intro keeps the lifecycle pipeline and the choice already made", async ({
    page,
  }) => {
    await authenticate(page);
    // A reader who collapsed the lifecycle header before this component existed.
    await page.goto(`${PORTAL}/knowledge`);
    await page.evaluate(() => localStorage.setItem("knowledge.lifecycle.expanded", "0"));
    await page.reload();

    await expect(toggle(page)).toHaveAttribute("aria-expanded", "false");
    const card = page.getByTestId("section-intro");
    for (const stage of ["Memory", "Insight", "Knowledge"]) {
      await expect(card).toContainText(stage);
    }
    await expect(card).toContainText("captured automatically");
  });

  test("an intro is offered on no detail page beneath a section", async ({ page }) => {
    await authenticate(page);
    for (const path of [
      "/assets/ast-001",
      "/prompts/prompt-003",
      "/scripts/script-001",
      // The hub renders one knowledge page, but that is a detail view: its own
      // Hide control is the one a reader means there.
      "/knowledge/pages/kp-seed-1",
    ]) {
      await page.goto(`${PORTAL}${path}`);
      await page.waitForLoadState("networkidle");
      await expect(page.getByTestId("section-intro")).toHaveCount(0);
    }
  });

  // Criterion 3: the intro must not cost the reader the controls they came for.
  for (const theme of ["light", "dark"] as const) {
    test(`every section's own controls stay above the fold at 1280x800 in ${theme}`, async ({
      page,
    }) => {
      await page.setViewportSize({ width: 1280, height: 800 });
      await authenticate(page);
      await applyTheme(page, theme);

      for (const intro of SECTION_INTROS) {
        await page.goto(`${PORTAL}${intro.path === "/" ? "/" : intro.path}`);
        await expect(page.getByTestId("section-intro")).toBeVisible();
        await page.waitForLoadState("networkidle");

        // The page's own root is the element after the intro in <main>; its
        // first block is the section's controls (a tab strip, a filter bar, a
        // header). Expanded is the worst case, and it is the default here.
        const firstBlock = page.locator("main > *").nth(1).locator(":scope > *").first();
        const box = await firstBlock.boundingBox();
        expect(box, `${intro.path}: no content after the intro`).not.toBeNull();
        expect(
          box!.y + box!.height,
          `${intro.path}: the section's controls are pushed below 800px`,
        ).toBeLessThanOrEqual(800);
      }
    });
  }
});
