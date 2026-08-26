import { type ScreenshotRoute } from "./route-types";

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
      const persona = page.locator("text=Finance Executive").first();
      if (await persona.isVisible()) await persona.click();
      await page.waitForTimeout(500);
      const tab = page.locator("button:has-text('API endpoints')").first();
      if (await tab.isVisible()) await tab.click();
      await page.waitForTimeout(600);
      // Select an operation so the rail shows the rule that decided it, which
      // is the half of the surface an empty trace panel does not document.
      const op = page.locator('[aria-label="GET /v1/invoices"]').first();
      if (await op.isVisible()) await op.click();
      await page.waitForTimeout(400);
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
