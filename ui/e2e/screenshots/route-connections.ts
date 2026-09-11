import { type ScreenshotRoute } from "./route-types";

// Every connection capture: the list, the editor, the two credential blocks
// whose fields only one auth mode has, the connection carrying both OAuth
// config vocabularies, and the create form. They live beside the manifest for
// the same reason the persona and script routes do -- one page's states kept
// together, and the manifest kept under its line budget.
export const adminConnectionRoutes: ScreenshotRoute[] = [
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
    // The OAuth 2.1 credential block (#1681), captured on an api connection
    // stored in the canonical shape: auth_mode "oauth" with the grant in its
    // own field. The editor read only the earlier oauth2_* spelling, so this
    // connection opened with no auth configuration at all.
    slug: "admin-connection-oauth",
    path: "/portal/admin/connections?kind=api&name=acme-billing-api",
    category: "admin",
    beforeCapture: async (page) => {
      const edit = page.locator("button:has-text('Edit')").first();
      if (await edit.isVisible()) {
        await edit.click();
        await page.waitForTimeout(600);
      }
      // The block is taller than the frame, so the capture is anchored where
      // it reads as one form: the grant picker down to the endpoint auth style.
      const style = page.locator("text=Endpoint auth style").first();
      if (await style.isVisible()) {
        await style.scrollIntoViewIfNeeded();
        await page.waitForTimeout(300);
      }
    },
  },
  {
    // A connection carrying both OAuth config vocabularies (#1682). The values
    // nothing reads are struck through and badged rather than listed beside the
    // live ones as equals, which is the state an operator has to be able to see.
    slug: "admin-connection-oauth-shadowed",
    path: "/portal/admin/connections?kind=api&name=acme-analytics-api",
    category: "admin",
    beforeCapture: async (page) => {
      // Anchored on the configuration table, where the struck-through values
      // are: the status card's warning sits just above it.
      const shadowed = page
        .locator("text=OAuth Client ID (legacy key)")
        .first();
      if (await shadowed.isVisible()) {
        await shadowed.scrollIntoViewIfNeeded();
        await page.waitForTimeout(300);
      }
    },
  },
  {
    // The signed_jwt credential block (#1648), captured on the connection whose
    // upstream needs it: the algorithm picker and the claims the upstream
    // registered, which no other auth mode has.
    slug: "admin-connection-signed-jwt",
    // The panel reads its selection from the query string, which selects the
    // connection without depending on where its row lands in the sidebar.
    path: "/portal/admin/connections?kind=api&name=acme-erp-api",
    category: "admin",
    beforeCapture: async (page) => {
      const edit = page.locator("button:has-text('Edit')").first();
      if (await edit.isVisible()) {
        await edit.click();
        await page.waitForTimeout(600);
      }
      // The credential block is taller than the frame, so the capture is
      // anchored where it reads as one form: the algorithm picker and the key
      // material at the top, down to the two timings.
      const issuer = page.locator("text=Issuer (iss)").first();
      if (await issuer.isVisible()) {
        await issuer.scrollIntoViewIfNeeded();
        await page.waitForTimeout(300);
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
];
