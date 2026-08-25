import { type ScreenshotRoute } from "./route-types";

// The operation browser (#1478), captured on both of its mounts. They live
// beside the manifest for the same reason the asset-viewer and scratch-table
// routes do: a page's captures kept together, so one added for a new state sits
// next to the others of the same page.
//
// Every one is deep-linked to an operation rather than left on the index. An
// empty pane says nothing about what the page is for, and the operation chosen
// carries parameters, a request body and several responses so the capture shows
// the shape a reader actually comes here to read.

export const apiBrowserUserRoutes: ScreenshotRoute[] = [
  // The caller's operation browser (#1478): the api connections their persona
  // reaches and what each one exposes. Deep-linked to an operation carrying
  // parameters and a response shape so the pane shows what it is for, and to
  // the call snippet a non-MCP client copies.
  {
    slug: "apis",
    path: "/portal/apis?connection=acme-billing&spec=core&op=listCustomers",
    category: "user",
  },
  // The same page scrolled to what a non-MCP client comes here for: the curl
  // against the gateway route, with the operation's method, path and body
  // already filled in.
  {
    slug: "apis-call-snippet",
    path: "/portal/apis?connection=acme-billing&spec=core&op=createCustomer",
    category: "user",
    beforeCapture: async (page) => {
      // The pane scrolls, and the snippet is the last thing in it: bring its
      // END into view, so the capture shows the whole command rather than its
      // first line.
      const snippet = page.locator("pre", { hasText: "curl -sS" }).first();
      await snippet.waitFor();
      await snippet.evaluate((el) => el.scrollIntoView({ block: "end" }));
      await page.waitForTimeout(300);
    },
  },
];

export const apiBrowserAdminRoutes: ScreenshotRoute[] = [
  // The operator's operation browser (#1478): every catalog and spec, including
  // the ones no connection references yet. Deep-linked to an operation with a
  // request body and several responses, because an empty pane would say nothing
  // about what the page is for.
  {
    slug: "admin-apis",
    path: "/portal/admin/apis?catalog=stripe-api-2025-01&spec=core&op=createCustomer",
    category: "admin",
  },
];
