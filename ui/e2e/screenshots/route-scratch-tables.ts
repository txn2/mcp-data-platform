import { type ScreenshotRoute } from "./route-types";

// The Scratch Tables section (#1472): the listing, and one registration at its
// own address in the two states a reader meets it in. They live beside the
// manifest for the same reason the asset-viewer and managed-script routes do --
// a page's captures kept together, so one added for a new state sits next to
// the others of the same page.
export const scratchTableRoutes: ScreenshotRoute[] = [
  {
    // Every scratch table a deployment has registered, in one list. The only
    // surface that answers what is in the shared schema without opening every
    // file in turn. The fixtures cover all four states a row takes: current,
    // behind its file, over a file that is gone, and somebody else's.
    slug: "scratch-tables",
    path: "/portal/scratch-tables",
    category: "user",
  },
  {
    // One registration: what to query, the columns with their types, the file
    // behind it, and the directory it reads. Opened on the reader's own
    // current registration, so the capture carries the action they are
    // offered on a table of theirs.
    slug: "scratch-table-detail",
    path: "/portal/scratch-tables/reg_2f1c8a",
    category: "user",
  },
  {
    // The same page on a pinned registration whose file has moved on since:
    // the staleness verdict and what to do about it, which is the second
    // thing only a cross-source read can tell a reader.
    slug: "scratch-table-stale",
    path: "/portal/scratch-tables/reg_7b3d90",
    category: "user",
  },
  {
    // A registration that follows its file and could not be moved onto the
    // current version (#1536): behind the file with the reason the follow
    // recorded, which is the state a failed coordinator leaves.
    slug: "scratch-table-follow-failed",
    path: "/portal/scratch-tables/reg_e19c42",
    category: "user",
  },
];
