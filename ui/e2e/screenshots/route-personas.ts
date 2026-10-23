import type { Page } from "@playwright/test";
import { type ScreenshotRoute } from "./route-types";

/** openPersona selects a persona and jumps to one section of its page (#2257). */
async function openPersona(page: Page, displayName: string, section?: string): Promise<void> {
  const persona = page.locator(`text=${displayName}`).first();
  if (await persona.isVisible()) await persona.click();
  await page.waitForTimeout(600);
  if (section) {
    const link = page.getByRole("navigation", { name: "Persona sections" }).getByRole("link", { name: section });
    if (await link.isVisible()) await link.click();
    await page.waitForTimeout(600);
  }
}

// Every persona-editor capture: the list with a persona open, the API-endpoint
// scope where route rules are written (#1479), and the create form. They live
// beside the manifest for the same reason the managed-resource and script
// routes do — one page's states kept together, and the manifest kept under its
// line budget.
export const adminPersonaRoutes: ScreenshotRoute[] = [
  {
    slug: "admin-personas",
    path: "/portal/admin/personas",
    category: "admin",
    beforeCapture: async (page) => {
      const de = page.locator("text=Data Engineer").first();
      if (await de.isVisible()) await de.click();
      await page.waitForTimeout(500);
    },
  },
  {
    // The persona editor's API-endpoint scope (#1479): the operations of each
    // api-kind connection with the persona's decision on each one. The finance
    // persona is the fixture that carries route rules, so it is the one that
    // shows a narrowed connection rather than an open one.
    slug: "admin-persona-api-routes",
    path: "/portal/admin/personas",
    category: "admin",
    beforeCapture: async (page) => {
      await openPersona(page, "Finance Executive", "API endpoints");
      // Select an operation so the rail shows the rule that decided it, which
      // is the half of the surface an empty trace panel does not document.
      const op = page.locator('[aria-label="GET /v1/invoices"]').first();
      if (await op.isVisible()) await op.click();
      await page.waitForTimeout(400);
    },
  },
  {
    // What the persona reaches as the server computes it from the page, saved
    // or not, and what is granted to it elsewhere (#2257).
    slug: "admin-persona-summary",
    path: "/portal/admin/personas",
    category: "admin",
    beforeCapture: (page) => openPersona(page, "Data Engineer", "Access summary"),
  },
  {
    slug: "admin-persona-portal",
    path: "/portal/admin/personas",
    category: "admin",
    beforeCapture: (page) => openPersona(page, "Data Engineer", "Portal access"),
  },
  {
    // An external persona: the rows External sets are locked with the saved
    // choice beside them, and its uploads are named (#2257, #2258).
    slug: "admin-persona-external",
    path: "/portal/admin/personas",
    category: "admin",
    beforeCapture: async (page) => {
      await openPersona(page, "Store Manager");
      const external = page.getByLabel(/External people/).first();
      if (await external.isVisible()) await external.click();
      await page.waitForTimeout(800);
      const link = page.getByRole("navigation", { name: "Persona sections" }).getByRole("link", { name: "Portal access" });
      if (await link.isVisible()) await link.click();
      await page.waitForTimeout(600);
    },
  },
  {
    // Deleting a persona lists what still names it (#2257).
    slug: "admin-persona-delete",
    path: "/portal/admin/personas",
    category: "admin",
    beforeCapture: async (page) => {
      await openPersona(page, "Data Engineer");
      await page.waitForTimeout(600);
      const del = page.getByRole("button", { name: "Delete" }).first();
      if (await del.isVisible()) await del.click();
      await page.waitForTimeout(500);
    },
  },
  {
    slug: "admin-persona-create",
    path: "/portal/admin/personas",
    category: "admin",
    beforeCapture: async (page) => {
      const btn = page.locator("button:has-text('New Persona')").first();
      if (await btn.isVisible()) {
        await btn.click();
        await page.waitForTimeout(600);
      }
    },
  },
];
