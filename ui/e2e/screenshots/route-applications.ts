import { type Page } from "@playwright/test";
import { siteRoutes } from "./route-sites";
import { type ScreenshotRoute } from "./route-types";

// openLargeDiagram opens the warehouse application's Diagram tab: twenty
// automations over twenty tables, drawn as a large application (#2700).
async function openLargeDiagram(page: Page) {
  await page.getByRole("tab", { name: "Diagram" }).click();
  await page.getByTestId("application-diagram-large").waitFor();
}

// backToList puts the Parts card back on List, the tab it remembers, so the
// captures after these open where they expect to.
async function backToList(page: Page) {
  await page.getByRole("tab", { name: "List" }).click();
}

// The large diagram's states (#2700): nothing selected (the band), an
// automation selected, a table selected, and the Table view.
const largeDiagramRoutes: ScreenshotRoute[] = [
  {
    slug: "application-diagram-large",
    path: "/portal/applications/app-warehouse",
    category: "user",
    beforeCapture: openLargeDiagram,
    afterCapture: backToList,
  },
  {
    slug: "application-diagram-large-automation",
    path: "/portal/applications/app-warehouse",
    category: "user",
    beforeCapture: async (page) => {
      await openLargeDiagram(page);
      await page.getByRole("button", { name: /^Automation Post refunds/ }).click();
      await page.getByTestId("diagram-panel").waitFor();
    },
    afterCapture: backToList,
  },
  {
    slug: "application-diagram-large-table",
    path: "/portal/applications/app-warehouse",
    category: "user",
    beforeCapture: async (page) => {
      await openLargeDiagram(page);
      await page.getByRole("button", { name: /^Table refund:/ }).click();
      await page.getByTestId("diagram-panel").waitFor();
    },
    afterCapture: backToList,
  },
  {
    slug: "application-diagram-table-view",
    path: "/portal/applications/app-warehouse",
    category: "user",
    beforeCapture: async (page) => {
      await openLargeDiagram(page);
      await page.getByRole("group", { name: "View" }).getByRole("button", { name: "Table" }).click();
      await page.getByTestId("diagram-table-view").waitFor();
    },
    afterCapture: backToList,
  },
];

// Each environment its own schema (#2709): the add dialog with its schema,
// the promote dialog's list after a release lands in dev, Add parts holding a
// site environment of no environment's name, and an application still naming
// its schema with the prepare banner and the change it reviews.
// settled waits out a dialog's open animation: the capture is taken as soon
// as beforeCapture returns.
async function settled(page: Page): Promise<void> {
  await page.getByRole("dialog").last().waitFor();
  await page.waitForTimeout(400);
}

const environmentRoutes: ScreenshotRoute[] = [
  {
    slug: "application-add-environment",
    path: "/portal/applications/app-shop",
    category: "user",
    beforeCapture: async (page) => {
      await page.getByRole("button", { name: "Add environment", exact: true }).click();
      await page.getByRole("dialog", { name: "Add environment" }).getByLabel("Name").fill("staging");
      await settled(page);
    },
  },
  {
    slug: "application-promote",
    path: "/portal/applications/app-shop",
    category: "user",
    beforeCapture: async (page) => {
      await page.getByRole("button", { name: "Release", exact: true }).click();
      await page.getByRole("dialog").getByRole("button", { name: "Release", exact: true }).click();
      await page.getByLabel("dev runs release 4").waitFor();
      await page.getByRole("button", { name: "Promote dev to prod" }).click();
      await page.getByTestId("promote-list").waitFor();
      await settled(page);
    },
  },
  {
    slug: "application-add-parts-sites",
    path: "/portal/applications/app-shop",
    category: "user",
    beforeCapture: async (page) => {
      await page.getByRole("button", { name: "Add parts" }).click();
      await page.getByRole("dialog", { name: "Add parts to shop" }).getByRole("tab", { name: /^Sites/ }).click();
      await page.getByTestId("site-unmatched-shop-web/preview").waitFor();
      await settled(page);
    },
  },
  {
    slug: "application-prepare-banner",
    path: "/portal/applications/app-warehouse",
    category: "user",
    waitFor: '[data-testid="prepare-banner"]',
  },
  {
    slug: "application-prepare",
    path: "/portal/applications/app-warehouse",
    category: "user",
    beforeCapture: async (page) => {
      await page.getByTestId("prepare-banner").getByRole("button", { name: "Review the change" }).click();
      await page.getByTestId("prepare-files").waitFor();
      await settled(page);
    },
  },
];

// Applications (#2411): the listing, and one released application with its
// parts, what a release now would change, and its releases with the changes
// each made. The mock's "shop" application is released three times, the last a
// rollback, so the Releases section shows every kind of entry it can hold.
export const applicationRoutes: ScreenshotRoute[] = [
  {
    slug: "applications",
    path: "/portal/applications",
    category: "user",
  },
  {
    slug: "application-detail",
    path: "/portal/applications/app-shop",
    category: "user",
  },
  // The Add parts dialog (#2527): a search by name across automations,
  // assets and webhook sources, one part held by another application.
  {
    slug: "application-add-parts",
    path: "/portal/applications/app-intake",
    category: "user",
    beforeCapture: async (page) => {
      await page.getByRole("button", { name: "Add parts" }).click();
      await page.getByRole("dialog", { name: "Add parts to intake" }).getByText("Already part of").waitFor();
    },
  },
  // The Approvals queue (#2494): a pending request for a form's public use,
  // with what changed since the version approved before.
  {
    slug: "approvals",
    path: "/portal/approvals",
    category: "user",
  },
  ...largeDiagramRoutes,
  ...environmentRoutes,
  ...siteRoutes,
];
