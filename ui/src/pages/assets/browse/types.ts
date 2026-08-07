import type { Asset } from "@/api/portal/types";
import type { ShareMeta } from "@/components/listView";

/** An asset for display, optionally carrying share-with-me metadata. */
export interface DisplayAsset {
  asset: Asset;
  share?: ShareMeta;
}
