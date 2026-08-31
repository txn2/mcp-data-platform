import { type Page } from "@playwright/test";
import { type ScreenshotRoute } from "./route-types";

/**
 * Every reader-facing section's intro in its collapsed state, light and dark
 * (#1570). The expanded state needs no capture of its own: a capture run starts
 * on a fresh origin, which is a reader who has never opened the section, so
 * every section's own screenshot already shows its intro open.
 *
 * The capture leaves each section expanded again, because the whole run shares
 * one origin and a section left collapsed here would be collapsed in every
 * later capture of that section.
 */
const SECTIONS: { slug: string; path: string }[] = [
  { slug: "assets", path: "/portal/" },
  { slug: "prompts", path: "/portal/prompts" },
  { slug: "scripts", path: "/portal/scripts" },
  { slug: "resources", path: "/portal/resources" },
  { slug: "scratch-tables", path: "/portal/scratch-tables" },
  { slug: "feedback", path: "/portal/feedback" },
  { slug: "knowledge", path: "/portal/knowledge" },
  { slug: "apis", path: "/portal/apis" },
  { slug: "activity", path: "/portal/activity" },
];

/**
 * Put the intro in `expanded`, whatever state the previous capture left it in.
 * Both button labels carry "this section is for", so one locator finds the
 * toggle in either state.
 */
async function setIntroExpanded(page: Page, expanded: boolean): Promise<void> {
  const toggle = page.getByRole("button", { name: /this section is for/i }).first();
  await toggle.waitFor({ state: "visible", timeout: 10_000 });
  if ((await toggle.getAttribute("aria-expanded")) !== String(expanded)) {
    await toggle.click();
    await page.waitForTimeout(500);
  }
}

export const sectionIntroRoutes: ScreenshotRoute[] = SECTIONS.map(({ slug, path }) => ({
  slug: `${slug}-intro-collapsed`,
  path,
  category: "user" as const,
  beforeCapture: (page: Page) => setIntroExpanded(page, false),
  afterCapture: (page: Page) => setIntroExpanded(page, true),
}));
