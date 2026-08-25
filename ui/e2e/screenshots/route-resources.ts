import {
  openGlossaryResourceTables,
  openResourceDetail,
  openResourceLifecycle,
  openTableRegisterForm,
  openTableRepairOffer,
  openTableRepaired,
  openCorrectedVersion,
} from "./route-actions";
import { openResourceUsedByAssets } from "./route-actions-refs";
import { type ScreenshotRoute } from "./route-types";

// Every managed-resource capture on the admin surface: the page as it opens,
// the lifecycle surfaces below its sidebar's fold, and the registered-table
// panel in both of the states a reader meets. They live beside the manifest for
// the same reason the asset-viewer and managed-script routes do — one library
// grown past what belongs inline, kept together so a capture added for one
// state sits next to the others of the same page.
export const adminResourceRoutes: ScreenshotRoute[] = [
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
    // The other half of the reference edge (#1475): the assets whose content
    // references this file, with the publicly shared one flagged. It is what
    // an owner reads before editing or deleting the file.
    slug: "resource-used-by-assets",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openResourceUsedByAssets,
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
