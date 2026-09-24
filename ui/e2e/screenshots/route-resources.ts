import { chooseUpload } from "./helpers/resources";
import {
  openResourceContextMenu,
  openResourceEmptyFolder,
  openResourceMultiSelect,
  openResourcePreview,
  openResourceSearch,
  openResourceSelection,
  openResourceSubfolder,
  openTopLevelFolder,
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
    // The Global top-level folder on the reader's own Resources page, where a
    // platform administrator is offered New folder and Upload (#1527). The
    // controls follow the caller's authority rather than which section the
    // page was mounted in.
    slug: "resources-global",
    path: "/portal/resources",
    category: "user",
    beforeCapture: async (page) => {
      await openTopLevelFolder(page, "global");
      // Waited on rather than timed out: a swallowed click would ship the
      // caller's own folder captioned as the global one.
      await page
        .getByRole("button", { name: "Upload", exact: true })
        .waitFor({ state: "visible", timeout: 5_000 });
      await page.waitForTimeout(400);
    },
  },
  {
    // The single-file upload dialog, from the path bar's Upload menu.
    slug: "resource-upload",
    path: "/portal/resources",
    category: "user",
    beforeCapture: async (page) => {
      await chooseUpload(page, "A file...");
      await page
        .getByRole("dialog", { name: "Upload Resource" })
        .waitFor({ state: "visible", timeout: 5_000 });
      await page.waitForTimeout(500);
    },
  },
  {
    // One file selected (#1872): a single click previews it in the pane beside
    // the listing -- its tile, where it is, its URI, its tags, and Open,
    // Download and Copy URI -- without leaving the folder.
    slug: "resources-preview",
    path: "/portal/resources",
    category: "user",
    beforeCapture: openResourcePreview,
  },
  {
    // Several files picked with Shift-click (#1872): the selection bar with
    // Move to, Tag and Delete over all of them, and the pane's count and total
    // size.
    slug: "resources-multi-select",
    path: "/portal/resources",
    category: "user",
    beforeCapture: openResourceMultiSelect,
  },
  {
    // The context menu on a file (#1872), at the pointer.
    slug: "resources-context-menu",
    path: "/portal/resources",
    category: "user",
    beforeCapture: openResourceContextMenu,
  },
  {
    // A folder stored with nothing in it (#1872): it is listed, it opens, and
    // it says where a drop or an upload into it is filed.
    slug: "resources-empty-folder",
    path: "/portal/resources",
    category: "user",
    beforeCapture: openResourceEmptyFolder,
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
    // The file manager as it opens (#1530, #1872): the folder tree on the
    // left, the caller's own folders in the listing, and the preview pane
    // describing the folder in view.
    slug: "resource-tree",
    path: "/portal/admin/resources",
    category: "admin",
  },
  {
    // Two levels in: the tree with the folder open and its ancestors expanded,
    // and the path bar naming each level. Each level is an address of its own,
    // so this view can be linked to and Back steps out one folder.
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
    // A search typed inside one folder spans the whole top-level folder: hits
    // from more than one folder, each naming where it is, with Show in folder.
    // Captured with a term that MATCHES --
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
    // The top-level folder a resource is filed in, as an editable field
    // (#1502). It was chosen once on the upload form and never again, so the
    // only route from a personal folder to a shared one was to upload the file a second time --
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
