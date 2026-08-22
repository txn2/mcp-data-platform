import {
  openGlossaryResourceTables,
  openResourceDetail,
  openResourceLifecycle,
  openTableRegisterForm,
} from "./route-actions";
import { type ScreenshotRoute } from "./route-types";

// Every managed-resource capture on the admin surface: the dialog as it opens,
// the lifecycle surfaces below its fold, and the registered-table panel in both
// of the states a reader meets. They live beside the manifest for the same
// reason the asset-viewer and managed-script routes do — one library grown past
// what belongs inline, kept together so a capture added for one state sits next
// to the others of the same dialog.
export const adminResourceRoutes: ScreenshotRoute[] = [
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
    // The register form over the same dialog: the connections this person can
    // register onto, and what the table is called.
    slug: "resource-table-register",
    path: "/portal/admin/resources",
    category: "admin",
    beforeCapture: openTableRegisterForm,
  },
];
