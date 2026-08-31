import {
  openAssetProducers,
  openAssetRefPicker,
  openAssetRefPickerAssets,
  openAssetRefs,
  openAssetThumbnail,
  openAssetUsedBy,
} from "./route-actions-refs";
import {
  openAssetMetadataEdit,
  openAssetProvenance,
  openAssetProvenanceCall,
  openAssetProvenanceEarlier,
  openAssetTables,
  openAssetVersionPicker,
} from "./route-actions";
import { type ScreenshotRoute } from "./route-types";

// Every asset-viewer capture: one per content type, the shared and
// collection-scoped variants, and the provenance panel (#1320) with one of its
// captured calls opened. They live beside the manifest for the same reason the
// script and drawer routes do — one surface's captures are read together, and
// the manifest stays a table of contents rather than a file that grows without
// bound.
export const assetViewerRoutes: ScreenshotRoute[] = [
  {
    slug: "asset-html",
    path: "/portal/assets/ast-001",
    category: "user",
  },
  {
    // Provenance panel (#1320): what the asset was built from, grouped by the
    // write that captured it. Lives behind the metadata sidebar, so the plain
    // asset-html capture never shows it.
    slug: "asset-provenance",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openAssetProvenance,
  },
  {
    // The captures behind the disclosure, with one of them opened. An asset a
    // scheduled script refreshes gets a capture per run, so the panel leads
    // with the newest and opens the rest one at a time (#1422).
    slug: "asset-provenance-earlier",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openAssetProvenanceEarlier,
  },
  {
    // One captured call opened: the statement, the purpose the agent stated
    // for it, the outcome, and the reference that names it in the audit log.
    slug: "asset-provenance-call",
    path: "/portal/assets/ast-003",
    category: "user",
    beforeCapture: openAssetProvenanceCall,
  },
  {
    // The version list open: every version dated, so an asset written on a
    // schedule can be navigated by when rather than by number (#1422).
    slug: "asset-versions",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openAssetVersionPicker,
  },
  {
    // What this asset's content references (#1475, #1488): the files and the
    // assets it depends on, the reference its markup has to name, and where
    // that reference is already written. Behind the metadata sidebar, like the
    // panels above.
    slug: "asset-refs",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openAssetRefs,
  },
  {
    // The picker open, which is the one state naming what a reference gives
    // away and who this asset is shared with before anything is added.
    slug: "asset-ref-picker",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openAssetRefPicker,
  },
  {
    // The picker's second tab: the assets this reader can reference (#1488),
    // which is how a dashboard comes to read a data file another job refreshes.
    slug: "asset-ref-picker-assets",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openAssetRefPickerAssets,
  },
  {
    // The reverse view on an asset: which reports read this one's content
    // (#1488), so an edit or a delete is made knowing what serves from it.
    slug: "asset-used-by",
    path: "/portal/assets/ast-004",
    category: "user",
    beforeCapture: openAssetUsedBy,
  },
  {
    // What wrote this asset (#1569): the script that refreshes it hourly and
    // the person who edits it, with the creator distinguished from the
    // modifier. It sits beside the provenance panel, which answers the other
    // question -- which data calls the content was built from.
    slug: "asset-producers",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openAssetProducers,
  },
  {
    // The tile everyone else sees of this asset, and the way back from one that
    // shows the wrong thing (#1497).
    slug: "asset-thumbnail",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openAssetThumbnail,
  },
  {
    // The metadata sidebar in edit mode on an asset that keeps a cap of its
    // own: the retention control in the one mode with a count behind it (#1421).
    slug: "asset-metadata-edit",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: openAssetMetadataEdit,
  },
  {
    // The same form on an asset with no override, where retention reads
    // "Deployment default" and no count is asked for.
    slug: "asset-metadata-edit-inherited",
    path: "/portal/assets/ast-002",
    category: "user",
    beforeCapture: openAssetMetadataEdit,
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
    // The registered-table panel on a CSV asset: where to query the file, the
    // columns it came back with, and the way to drop it again (#1327).
    slug: "asset-table",
    path: "/portal/assets/ast-008",
    category: "user",
    beforeCapture: openAssetTables,
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
];
