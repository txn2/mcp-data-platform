import { assetViewerRoutes } from "./route-assets";
import { drawerRoutes } from "./route-drawers";
import { adminScriptRoutes, userScriptRoutes } from "./route-scripts";
import {
  openCollectionDetailsDialog,
  openFeedbackMentionComposer,
  openKnowledgeGraph,
  openKnowledgeGraphCorpus,
  openPersonaScopeTab,
  openResourceDetail,
  openResourceLifecycle,
  openScriptAlertSettings,
  openShareDialog,
  openShareDialogPublicLink,
  openShareDialogWithRecipient,
} from "./route-actions";
import { type ScreenshotRoute } from "./route-types";

export type { ScreenshotRoute };

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
    // The reader's own sessions, the second face of the Activity section.
    slug: "my-sessions",
    path: "/portal/activity/sessions",
    category: "user",
  },
  {
    // The reader's own recorded calls, the third face of the Activity
    // section: what they ran, why, and what came of it.
    slug: "my-calls",
    path: "/portal/activity/calls",
    category: "user",
  },
  {
    slug: "my-assets",
    path: "/portal/",
    category: "user",
  },
  {
    // Assets page in the Shared ownership scope (replaces the removed
    // standalone Shared With Me page, consolidated in #616). The clicked
    // scope persists in localStorage (shared with Collections), so clear it
    // after clicking to keep later captures order-independent.
    slug: "assets-shared",
    path: "/portal/",
    category: "user",
    beforeCapture: async (page) => {
      // Unconditional click: this capture's whole identity is the Shared
      // scope, so a missing tab must fail the run, not silently publish an
      // All-scope screenshot under the assets-shared name.
      await page.getByRole("tab", { name: "Shared" }).click();
      await page.waitForTimeout(500);
      await page.evaluate(() => localStorage.removeItem("asset-scope"));
    },
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
    beforeCapture: openPersonaScopeTab,
  },
  {
    // Resource upload modal.
    slug: "resource-upload",
    path: "/portal/resources",
    category: "user",
    beforeCapture: async (page) => {
      // Open the Upload modal via the always-visible header "Upload" button
      // (the empty-state "Upload Resource" button is absent once resources
      // are populated, which previously left this capture showing the list).
      await page
        .getByRole("button", { name: "Upload", exact: true })
        .first()
        .click({ timeout: 3_000 })
        .catch(() => {});
      await page.waitForTimeout(700);
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
      // Both clicks auto-wait. An `isVisible()` guard does not retry, so with
      // the panel or its thread list a frame late the step was skipped without
      // error and the detail slug captured the list — or the bare viewer.
      await page.getByRole("main").getByRole("button", { name: /Feedback/ }).first().click();
      await page.getByRole("button", { name: /We don't use that term/ }).click();
      await page.waitForTimeout(500);
    },
  },
  {
    // Mention composer (#627): the audience-scoped type-ahead open over the
    // new-feedback form.
    slug: "asset-feedback-mention",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openFeedbackMentionComposer,
  },
  {
    // KnowledgeHub (#661): one /knowledge route, three hash-driven tabs plus
    // the review queue, which is addressable on its own so the review-queue
    // alert email can link straight to it (#803). The tabs expand to
    // knowledge-{knowledge,insights,memory,review} captures, so no standalone
    // #insights / #memory entries (they would collide).
    slug: "knowledge",
    path: "/portal/knowledge",
    category: "user",
    tabs: ["knowledge", "insights", "memory", "review"],
    beforeCapture: async (page) => {
      // On the "Search All" sub-tab (knowledge tab default) type a query so the
      // capture shows grouped federated results with a coverage summary instead
      // of an empty prompt. The search box is absent on the insights/memory
      // tabs, so this no-ops there.
      const search = page.getByPlaceholder(/Search all knowledge/i);
      if (await search.isVisible().catch(() => false)) {
        await search.fill("revenue");
        await page.waitForTimeout(900);
      }
    },
  },
  // The Catalog section (#1194): the one route holding every DataHub-backed
  // surface, captured for its inner tab row and section-wide connection picker.
  { slug: "knowledge-catalog", path: "/portal/knowledge/catalog", category: "user" },
  {
    // The knowledge corpus drawn as a graph (#1162): the alternate layout to the
    // card list, showing which pages cluster around an entity and which stand alone.
    slug: "knowledge-graph",
    path: "/portal/knowledge/pages",
    category: "user",
    beforeCapture: openKnowledgeGraph,
  },
  {
    // The same graph in its whole-corpus overview, where the detected clusters
    // are drawn as regions rather than reported one node at a time.
    slug: "knowledge-graph-corpus",
    path: "/portal/knowledge/pages",
    category: "user",
    beforeCapture: openKnowledgeGraphCorpus,
  },
  {
    slug: "prompts",
    path: "/portal/prompts",
    category: "user",
  },
  ...userScriptRoutes,
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
    // Library bucket: approved shared prompts grouped by collection (#1010).
    slug: "prompts-library",
    path: "/portal/prompts",
    category: "user",
    beforeCapture: async (page) => {
      const tab = page.getByRole("tab", { name: /^Library/ }).first();
      if (await tab.isVisible()) {
        await tab.click();
        await page.waitForTimeout(600);
      }
    },
  },
  {
    // Collections manager dialog (create/rename/delete groups) (#1010).
    slug: "prompt-collections",
    path: "/portal/prompts",
    category: "user",
    beforeCapture: async (page) => {
      const btn = page.locator("button:has-text('Collections')").first();
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
    // Library prompt viewer with version history, approval provenance, and a
    // pending draft awaiting review (#1010).
    slug: "prompt-view-library",
    path: "/portal/prompts/prompt-003",
    category: "user",
  },
  {
    // Version diff between an older snapshot and the served version (#1010).
    slug: "prompt-version-diff",
    path: "/portal/prompts/prompt-003",
    category: "user",
    beforeCapture: async (page) => {
      const btn = page.locator("button:has-text('Diff vs current')").last();
      if (await btn.isVisible()) {
        await btn.click();
        await page.waitForTimeout(600);
      }
    },
  },
  {
    // Share dialog (create link + share with users) on an asset, in its
    // default state: a link for signed-in users, which carries no lifetime.
    slug: "asset-share",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openShareDialog,
  },
  {
    // Share dialog switched to a public link, the only state showing the
    // anonymous-access warning and the lifetime control (#1279).
    slug: "asset-share-public",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openShareDialogPublicLink,
  },
  {
    // Share dialog with a recipient named, which is the only state that shows
    // the notify checkbox and the sharer's message box (#1016). Captured
    // separately because those controls are absent from the dialog above.
    slug: "asset-share-recipient",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openShareDialogWithRecipient,
  },

  ...assetViewerRoutes,
  {
    // Per-user settings: email notification preferences (#631).
    slug: "settings",
    path: "/portal/settings",
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
    slug: "admin-collections",
    path: "/portal/admin/collections",
    category: "admin",
  },
  {
    // The agent-owned collection: the case the admin surface exists for, since
    // no owner-scoped list can show it (#1292).
    slug: "admin-collection-detail",
    path: "/portal/admin/collections/col-agent-001",
    category: "admin",
  },
  {
    // The form behind Edit details: the two fields an admin may correct on a
    // collection they do not own.
    slug: "admin-collection-edit",
    path: "/portal/admin/collections/col-agent-001",
    category: "admin",
    beforeCapture: openCollectionDetailsDialog,
  },
  // ToolsPage is a master-detail view that keeps selection + active tab in URL
  // *search params* (?selected=&tab=), not the hash. So each detail tab is its
  // own route with the full query string baked into `path` (no `tabs` field).
  // The detail tabs (overview/tryit/activity/visibility) render for any tool;
  // enrichment only renders for gateway-proxied (mcp) tools, so it points at a
  // gateway tool with cross-enrichment rules.
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
    // AuditLogPage's real hash tabs are
    // mcp/apigateway/health/indexing/events/notifications (there is no
    // "overview" tab; the default is "mcp"). The "indexing" tab is where
    // IndexingPage renders and "notifications" is the email-delivery monitor.
    // Capturing all six keeps this in sync with the merged Dashboard activity
    // view.
    slug: "admin-audit",
    path: "/portal/admin/audit",
    category: "admin",
    tabs: ["mcp", "apigateway", "health", "indexing", "events", "notifications"],
  },
  {
    slug: "admin-sessions",
    path: "/portal/admin/sessions",
    category: "admin",
  },
  {
    // The operator's catalog of every recorded call, with the outcome each
    // was derived to and the reuse that argues for publishing it.
    slug: "admin-calls",
    path: "/portal/admin/calls",
    category: "admin",
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
    // Resource detail as it opens: what the resource is, its metadata, and the
    // inline preview. The dialog caps at the viewport and scrolls its body
    // (#1233), so the lifecycle panels below the fold are a second capture
    // rather than more of this one.
    slug: "resource-detail",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceDetail,
  },
  {
    // The same dialog scrolled to the lifecycle surfaces: the usage rollup, the
    // version history with its restore actions, and the prompts attaching the
    // resource (#1014). Opened on the fixture that carries a revision trail and
    // read activity, so those surfaces are populated rather than empty.
    slug: "resource-lifecycle",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceLifecycle,
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
    beforeCapture: openPersonaScopeTab,
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
  {
    // Platform settings: SMTP configuration for email notifications (#631)
    // and the two review-queue alerts that send through it (#803, #1287).
    slug: "admin-settings",
    path: "/portal/admin/settings",
    category: "admin",
  },
  {
    // The managed-script review alert section, which sits below the fold of a
    // viewport capture of the settings page (#1287).
    slug: "admin-settings-script-alert",
    path: "/portal/admin/settings",
    category: "admin",
    beforeCapture: openScriptAlertSettings,
  },
  ...adminScriptRoutes,

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

  ...drawerRoutes,
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
