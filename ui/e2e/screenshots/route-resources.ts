import {
  openResourceSearch,
  openResourceSelection,
  openResourceSubfolder,
} from "./route-actions-library";
import {
  openCorrectedVersion,
  openGlossaryResourceTables,
  openPersonaScopeTab,
  openResourceDetail,
  openResourceLifecycle,
  openResourceMove,
  openTableRegisterForm,
  openTableRepairOffer,
  openTableRepaired,
} from "./route-actions";
import {
  openResourceProducers,
  openResourceThumbnail,
  openResourceUsedByAssets,
} from "./route-actions-refs";
import { type ScreenshotRoute } from "./route-types";

// The reader's own Resources page, as against the administrator's section
// below. They live here rather than inline in the manifest for the reason the
// admin ones do: one surface grown past what belongs in a shared file.
export const userResourceRoutes: ScreenshotRoute[] = [
  {
    slug: "resources",
    path: "/portal/resources",
    category: "user",
    beforeCapture: openPersonaScopeTab,
  },
  {
    // The library as it opens (#1553): every library the reader can reach at
    // once, the ten files that changed last above the folders, and each file
    // as a card rather than a row. It is the default view, so it is captured
    // with nothing done to it.
    slug: "resources-recent",
    path: "/portal/resources",
    category: "user",
    beforeCapture: async (page) => {
      await page
        .getByTestId("recent-resources")
        .waitFor({ state: "visible", timeout: 5_000 })
        .catch(() => {});
      await page.waitForTimeout(400);
    },
  },
  {
    // The global library on the reader's own Resources page, where a platform
    // administrator is offered Upload (#1527). The control follows the caller's
    // authority rather than which section the page was mounted in, so the
    // library an administrator can publish to says so on the page they were
    // already reading it on.
    slug: "resources-global",
    path: "/portal/resources",
    category: "user",
    beforeCapture: async (page) => {
      await page.getByRole("combobox", { name: "Library" }).click({ timeout: 3_000 });
      await page.getByRole("option", { name: "Global", exact: true }).click({ timeout: 3_000 });
      // Waited on rather than timed out: a swallowed click would ship the
      // caller's own library captioned as the global one, which is the opposite
      // of what this documents.
      await page
        .getByRole("button", { name: "Upload", exact: true })
        .waitFor({ state: "visible", timeout: 5_000 });
      await page.waitForTimeout(400);
    },
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
];

// Every managed-resource capture on the admin surface: the page as it opens,
// the lifecycle surfaces below its sidebar's fold, and the registered-table
// panel in both of the states a reader meets. They live beside the manifest for
// the same reason the asset-viewer and managed-script routes do — one library
// grown past what belongs inline, kept together so a capture added for one
// state sits next to the others of the same page.
export const adminResourceRoutes: ScreenshotRoute[] = [
  {
    // The library as a tree (#1530). It was one expandable section per flat
    // category with every file in the section, which at a thousand files is
    // six unbounded lists and a search box.
    slug: "resource-tree",
    path: "/portal/admin/resources",
    category: "admin",
  },
  {
    // Two levels in. Each level is an address of its own, so this view can be
    // linked to and Back steps out one folder rather than out of the library.
    slug: "resource-folder",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceSubfolder,
  },
  {
    // Several files picked and one move over all of them, reporting what it did
    // to each. Re-filing forty resources meant opening forty Edit dialogs.
    slug: "resource-multi-select-move",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceSelection,
  },
  {
    // Search across the whole library: hits from more than one folder, each
    // naming the path it was found at. Captured with a term that MATCHES --
    // the sentence this illustrates is about what a hit shows, which a
    // no-result search cannot demonstrate.
    slug: "resource-search",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceSearch,
  },
  {
    // Resource detail as it opens: the content at the page's own width, with
    // what the resource is beside it (#1470). The sidebar scrolls within its
    // column, so the lifecycle panels below its fold are a second capture
    // rather than more of this one.
    slug: "resource-detail",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceDetail,
  },
  {
    // The same page's sidebar scrolled to the lifecycle surfaces: the usage
    // rollup, the version history with its restore actions, and the prompts
    // attaching the resource (#1014). Opened on the fixture that carries a
    // revision trail and read activity, so those surfaces are populated rather
    // than empty.
    slug: "resource-lifecycle",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceLifecycle,
  },
  {
    // The tile everyone else sees of this file, and the way back from one that
    // shows the wrong thing (#1568). A managed resource is captured by the same
    // capturer as a portal asset and stored under the same rule, and had
    // neither the picture nor the button until this.
    slug: "resource-thumbnail",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceThumbnail,
  },
  {
    // The library a resource is filed in, as an editable field (#1502). It was
    // chosen once on the upload form and never again, so the only route from a
    // personal library to a shared one was to upload the file a second time --
    // which mints a second id, a second URI and a second blob, and leaves every
    // asset and prompt that referenced the first one referencing it.
    slug: "resource-move",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceMove,
  },
  {
    // The other half of the reference edge (#1475): the assets whose content
    // references this file, with the publicly shared one flagged. It is what
    // an owner reads before editing or deleting the file.
    slug: "resource-used-by-assets",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceUsedByAssets,
  },
  {
    // What wrote this file (#1569). A resource recorded only an uploader, and
    // for a managed-script run that was the script's NAME -- so a rename
    // severed the link and a script that replaced the content of a file it did
    // not upload left no trace. This is the record that survives both, with the
    // uploader and the refreshing script both listed.
    slug: "resource-producers",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceProducers,
  },
  {
    // The registered-table panel on a managed CSV resource, showing a
    // registration the file has moved on from: the table still serves the
    // revision it was registered against, which nothing about the rows says
    // (#1327).
    slug: "resource-table",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openGlossaryResourceTables,
  },
  {
    // The register form on the same page: the connections this person can
    // register onto, and what the table is called.
    slug: "resource-table-register",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openTableRegisterForm,
  },
  {
    // A CSV a query engine cannot read the way it is stored: its cells carry
    // line breaks, so every such row would be torn into fragments in a table
    // that reported no problem at all. The refusal names what is wrong and
    // offers the correction as a control (#1441).
    slug: "resource-table-repair-offer",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openTableRepairOffer,
  },
  {
    // The correction taken: the file has a new version, and the panel says what
    // changed in it -- the part that outlives the registration.
    slug: "resource-table-repaired",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openTableRepaired,
  },
  {
    // The same page's version history after the correction: the new version
    // says why the file changed, the version below it is what its owner
    // uploaded and says nothing. This is what a reader sees once the
    // registration's own answer is no longer on screen (#1450).
    slug: "resource-corrected-version",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openCorrectedVersion,
  },
];
