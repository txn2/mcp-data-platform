import type { Collection } from "@/api/portal/types";
import type { ShareMeta } from "@/components/listView";

/** A collection for display, optionally carrying share-with-me metadata. */
export interface DisplayCollection {
  collection: Collection;
  share?: ShareMeta;
}
