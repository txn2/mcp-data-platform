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
  {
    // Collection editor (drag-and-drop section/asset authoring). Rendering
    // only needs the GET (sections + resolved items), which the mock provides.
    slug: "collection-edit",
    path: "/portal/collections/col-001/edit",
    category: "user",
  },
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
    // Resource upload modal.
    slug: "resource-upload",
    path: "/portal/resources",
    category: "user",
    beforeCapture: async (page) => {
      const btn = page.locator("button:has-text('Upload Resource')").first();
      if (await btn.isVisible()) {
        await btn.click();
        await page.waitForTimeout(600);
      }
    },
  },
  {
    // Standalone feedback channel.
    slug: "feedback",
    path: "/portal/feedback",
    category: "user",
  },
  {
    // Per-asset feedback drawer, opened over the asset viewer.
    slug: "asset-feedback",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: async (page) => {
      // Scope to main so the toolbar button wins over the sidebar nav entry.
      const btn = page.getByRole("main").getByRole("button", { name: /Feedback/ }).first();
      if (await btn.isVisible()) {
        await btn.click();
        await page.waitForTimeout(500);
      }
    },
  },
  {
    // Feedback thread detail (timeline + reply + moderation).
    slug: "asset-feedback-detail",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: async (page) => {
      const btn = page.getByRole("main").getByRole("button", { name: /Feedback/ }).first();
      if (await btn.isVisible()) {
        await btn.click();
        const row = page.getByText("We don't use that term");
        if (await row.isVisible()) await row.click();
        await page.waitForTimeout(500);
      }
    },
  },
  {
    // KnowledgeHub (#661): one /knowledge route, three hash-driven tabs.
    slug: "knowledge",
    path: "/portal/knowledge",
    category: "user",
    tabs: ["knowledge", "insights", "memory"],
  },
  {
    slug: "knowledge-insights",
    path: "/portal/knowledge#insights",
    category: "user",
  },
  {
    slug: "knowledge-memory",
    path: "/portal/knowledge#memory",
    category: "user",
  },
  {
    slug: "prompts",
    path: "/portal/prompts",
    category: "user",
  },
  {
    // Personal prompt create form.
    slug: "prompt-create",
    path: "/portal/prompts",
    category: "user",
    beforeCapture: async (page) => {
      const btn = page.locator("button:has-text('New Prompt')").first();
      if (await btn.isVisible()) {
        await btn.click();
        await page.waitForTimeout(600);
      }
    },
  },
  {
    // User-facing prompt viewer (/prompts/:id). prompt-010 is a personal prompt.
    slug: "prompt-view",
    path: "/portal/prompts/prompt-010",
    category: "user",
  },
  {
    // Share dialog (create public link + share with users) on an asset.
    slug: "asset-share",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: async (page) => {
      const btn = page.locator("button:has-text('Share')").first();
      if (await btn.isVisible()) {
        await btn.click();
        await page.waitForTimeout(600);
      }
    },
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
    // Asset shared with the current user — opens in the standard viewer.
    slug: "shared-asset",
    path: "/portal/assets/ast-ext-001",
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
  // ToolsPage is a master-detail view that keeps selection + active tab in URL
  // *search params* (?selected=&tab=), not the hash. So each detail tab is its
  // own route with the full query string baked into `path` (no `tabs` field).
  // The detail tabs (overview/tryit/activity/visibility) render for any tool;
  // enrichment only renders for gateway-proxied (mcp) tools, so it points at a
  // gateway tool with cross-injection rules.
  {
    slug: "admin-tools-overview",
    path: "/portal/admin/tools?selected=trino_query",
    category: "admin",
  },
  {
    slug: "admin-tools-tryit",
    path: "/portal/admin/tools?selected=trino_query&tab=tryit",
    category: "admin",
  },
  {
    slug: "admin-tools-activity",
    path: "/portal/admin/tools?selected=trino_query&tab=activity",
    category: "admin",
  },
  {
    slug: "admin-tools-visibility",
    path: "/portal/admin/tools?selected=trino_query&tab=visibility",
    category: "admin",
  },
  {
    slug: "admin-tools-enrichment",
    path: "/portal/admin/tools?selected=crm_search_accounts&tab=enrichment",
    category: "admin",
  },
  {
    // AuditLogPage's real hash tabs are mcp/apigateway/health/indexing/events
    // (there is no "overview" tab; the default is "mcp"). The "indexing" tab is
    // where IndexingPage renders. Capturing all five keeps this in sync with the
    // merged Dashboard activity view.
    slug: "admin-audit",
    path: "/portal/admin/audit",
    category: "admin",
    tabs: ["mcp", "apigateway", "health", "indexing", "events"],
  },
  {
    slug: "admin-api-catalogs",
    path: "/portal/admin/api-catalogs",
    category: "admin",
  },
  // Config editors (CodeMirror MarkdownEditor). These were excluded over a
  // duplicate-@codemirror/state crash in headless mode, now fixed via
  // resolve.dedupe in vite.config.ts.
  {
    slug: "admin-description",
    path: "/portal/admin/description",
    category: "admin",
  },
  {
    slug: "admin-agent-instructions",
    path: "/portal/admin/agent-instructions",
    category: "admin",
  },
  {
    slug: "admin-connections",
    path: "/portal/admin/connections",
    category: "admin",
  },
  {
    // Connection editor (edit form). Select a connection, then open Edit.
    slug: "admin-connection-edit",
    path: "/portal/admin/connections",
    category: "admin",
    beforeCapture: async (page) => {
      const row = page.locator("text=acme-warehouse").first();
      if (await row.isVisible()) {
        await row.click();
        await page.waitForTimeout(400);
      }
      const edit = page.locator("button:has-text('Edit')").first();
      if (await edit.isVisible()) {
        await edit.click();
        await page.waitForTimeout(600);
      }
    },
  },
  {
    // Connection create form (new gateway/Trino/S3 connection).
    slug: "admin-connection-create",
    path: "/portal/admin/connections",
    category: "admin",
    beforeCapture: async (page) => {
      const add = page.locator("button:has-text('Add Connection')").first();
      if (await add.isVisible()) {
        await add.click();
        await page.waitForTimeout(600);
      }
    },
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
    slug: "admin-users",
    path: "/portal/admin/users",
    category: "admin",
  },
  {
    slug: "admin-changelog",
    path: "/portal/admin/changelog",
    category: "admin",
  },

  // =========================================================================
  // Editor / create forms — the rich authoring states behind the list views.
  // =========================================================================
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
  {
    slug: "admin-catalog-create",
    path: "/portal/admin/api-catalogs",
    category: "admin",
    beforeCapture: async (page) => {
      const btn = page.locator("button:has-text('New catalog')").first();
      if (await btn.isVisible()) {
        await btn.click();
        await page.waitForTimeout(600);
      }
    },
  },
  {
    slug: "admin-key-create",
    path: "/portal/admin/keys",
    category: "admin",
    beforeCapture: async (page) => {
      const btn = page.locator("button:has-text('Add Key')").first();
      if (await btn.isVisible()) {
        await btn.click();
        await page.waitForTimeout(600);
      }
    },
  },
  {
    slug: "admin-prompt-create",
    path: "/portal/admin/prompts",
    category: "admin",
    beforeCapture: async (page) => {
      const btn = page.locator("button:has-text('New Prompt')").first();
      if (await btn.isVisible()) {
        await btn.click();
        await page.waitForTimeout(600);
      }
    },
  },

  // =========================================================================
  // Drawer / detail-panel states (open via row click; no separate route).
  // =========================================================================
  {
    slug: "admin-audit-event-detail",
    path: "/portal/admin/audit#events",
    category: "admin",
    beforeCapture: async (page) => {
      // The click may no-op when a drawer is already open from the prior theme
      // (light/dark share one page and same-hash nav doesn't reload): the
      // drawer's overlay covers the rows. That's fine — the open drawer is
      // exactly what we want to capture, so swallow the click failure.
      const row = page.locator("table tbody tr").first();
      await row.click({ timeout: 2_000 }).catch(() => {});
      await page.waitForTimeout(600);
    },
  },
  {
    slug: "knowledge-insight-detail",
    path: "/portal/knowledge#insights",
    category: "admin",
    beforeCapture: async (page) => {
      // The click may no-op when a drawer is already open from the prior theme
      // (light/dark share one page and same-hash nav doesn't reload): the
      // drawer's overlay covers the rows. That's fine — the open drawer is
      // exactly what we want to capture, so swallow the click failure.
      const row = page.locator("table tbody tr").first();
      await row.click({ timeout: 2_000 }).catch(() => {});
      await page.waitForTimeout(600);
    },
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
  // No routes are currently excluded. Add a pageTitles key here (with a
  // documented reason) only when a route genuinely cannot be captured.
]);
