import { type Page } from "@playwright/test";

export interface ScreenshotRoute {
  slug: string;
  path: string;
  category: "user" | "admin";
  tabs?: string[];
  waitFor?: string;
  waitForThumbnails?: number;
  clientNav?: boolean;
  beforeCapture?: (page: Page) => Promise<void>;
}

export const routes: ScreenshotRoute[] = [
  // =========================================================================
  // User Portal Routes
  // =========================================================================

  {
    slug: "activity",
    path: "/portal/activity",
    category: "user",
  },
  {
    slug: "my-assets",
    path: "/portal/",
    category: "user",
  },
  {
    slug: "collections",
    path: "/portal/collections",
    category: "user",
  },
  {
    slug: "collection-view",
    path: "/portal/collections/col-001",
    category: "user",
  },
  // collection-edit excluded: requires section update handler that returns
  // the full collection with resolved items, which the mock doesn't support yet.
  // {
  //   slug: "collection-edit",
  //   path: "/portal/collections/col-001/edit",
  //   category: "user",
  // },
  {
    slug: "resources",
    path: "/portal/resources",
    category: "user",
    beforeCapture: async (page) => {
      const tab = page.locator("text=data-engineer").first();
      if (await tab.isVisible()) {
        await tab.click();
        await page.waitForTimeout(500);
      }
    },
  },
  {
    slug: "shared",
    path: "/portal/shared",
    category: "user",
  },
  {
    slug: "my-knowledge",
    path: "/portal/my-knowledge",
    category: "user",
  },
  {
    slug: "prompts",
    path: "/portal/prompts",
    category: "user",
  },

  // Asset viewer — one per content type
  {
    slug: "asset-html",
    path: "/portal/assets/ast-001",
    category: "user",
  },
  {
    slug: "asset-svg",
    path: "/portal/assets/ast-002",
    category: "user",
  },
  {
    slug: "asset-markdown",
    path: "/portal/assets/ast-003",
    category: "user",
  },
  {
    slug: "asset-jsx",
    path: "/portal/assets/ast-004",
    category: "user",
  },
  {
    slug: "asset-csv",
    path: "/portal/assets/ast-008",
    category: "user",
  },
  {
    slug: "shared-asset",
    path: "/portal/shared/assets/ast-ext-001",
    category: "user",
  },
  {
    slug: "collection-asset",
    path: "/portal/collections/col-001/assets/ast-001",
    category: "user",
  },

  // =========================================================================
  // Admin Routes
  // =========================================================================

  {
    slug: "admin-dashboard",
    path: "/portal/admin",
    category: "admin",
  },
  {
    slug: "admin-assets",
    path: "/portal/admin/assets",
    category: "admin",
  },
  {
    slug: "admin-asset-detail",
    path: "/portal/admin/assets/ast-007",
    category: "admin",
  },
  {
    slug: "admin-tools",
    path: "/portal/admin/tools",
    category: "admin",
    tabs: ["overview", "explore"],
    beforeCapture: async (page) => {
      const hash = new URL(page.url()).hash;
      if (hash === "#explore") {
        const toolItem = page.locator("text=Trino Query").first();
        if (await toolItem.isVisible()) {
          await toolItem.click();
          await page.waitForTimeout(1000);
        }
      }
    },
  },
  {
    slug: "admin-audit",
    path: "/portal/admin/audit",
    category: "admin",
    tabs: ["overview", "events"],
  },
  {
    slug: "admin-knowledge",
    path: "/portal/admin/knowledge",
    category: "admin",
    tabs: ["overview", "knowledge", "memory", "changesets"],
  },
  // admin-description and admin-agent-instructions are intentionally
  // excluded from screenshot capture (see excludedRoutes below) — the
  // MarkdownEditor (CodeMirror) crashes in headless mode due to
  // duplicate @codemirror/state instances. Re-enable once the portal
  // fixes this. The excludedRoutes registration below is what keeps
  // the route-sync test green: it documents the gap as intentional
  // rather than masking it as a missing manifest entry.
  {
    slug: "admin-connections",
    path: "/portal/admin/connections",
    category: "admin",
  },
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
    slug: "admin-prompts",
    path: "/portal/admin/prompts",
    category: "admin",
  },
  {
    slug: "admin-resources",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: async (page) => {
      const tab = page.locator("text=data-engineer").first();
      if (await tab.isVisible()) {
        await tab.click();
        await page.waitForTimeout(500);
      }
    },
  },
  {
    slug: "admin-keys",
    path: "/portal/admin/keys",
    category: "admin",
  },
  {
    slug: "admin-changelog",
    path: "/portal/admin/changelog",
    category: "admin",
  },
];

/**
 * Routes intentionally NOT captured in screenshot runs. Documented
 * here so the route-sync test can distinguish "missing manifest entry
 * (bug)" from "deliberately excluded (known infra constraint)."
 *
 * Each entry MUST include the AppShell pageTitles key (without the
 * /portal prefix) and a reason. When re-enabling a route, remove its
 * key from this set AND add a normal entry to `routes` above.
 */
export const excludedRoutes: ReadonlySet<string> = new Set([
  // CodeMirror crash in headless mode (duplicate @codemirror/state instances).
  "/admin/description",
  "/admin/agent-instructions",
]);
