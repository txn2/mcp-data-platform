import { useAsset, useCollection } from "@/api/portal/hooks";
import { useResource } from "@/api/resources/hooks";
import type { Resource } from "@/api/resources/types";
import type { Asset, Collection } from "@/api/portal/types";
import { assetThumbnailSrc, resourceThumbnailSrc } from "@/lib/thumbnailSupport";
import { useResolvedDark } from "@/stores/theme";
import type { ShareKind } from "./useShareData";

/** What the dialog names an item by, who owns it, and the tile a post previews. */
export interface ShareItem {
  name: string;
  ownerEmail: string;
  thumbnail?: string;
}

/**
 * useShareItem reads the item a share dialog is opened on (#2291), so the
 * title names it and People with access opens with its owner wherever the
 * dialog is opened from. A prompt is named by the page that opens it.
 */
export function useShareItem(kind: ShareKind, id: string, given: Partial<ShareItem>): ShareItem {
  const dark = useResolvedDark();
  const known = given.name !== undefined && given.ownerEmail !== undefined;
  const asset = useAsset(kind === "asset" && !known ? id : "").data;
  const collection = useCollection(kind === "collection" && !known ? id : "").data;
  const file = useResource(kind === "resource" && !known ? id : "").data;
  return { ...read({ asset, collection, file }, dark), ...given };
}

/** read is what the one item that loaded says about itself. */
function read(
  { asset, collection, file }: { asset?: Asset; collection?: Collection; file?: Resource },
  dark: boolean,
): ShareItem {
  if (asset) return { name: asset.name, ownerEmail: asset.owner_email, thumbnail: assetThumbnailSrc(asset, dark) };
  if (collection) return { name: collection.name, ownerEmail: collection.owner_email };
  if (file) return { name: file.display_name, ownerEmail: file.uploader_email, thumbnail: resourceThumbnailSrc(file, dark) };
  return { name: "", ownerEmail: "" };
}
