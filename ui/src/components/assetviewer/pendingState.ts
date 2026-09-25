import type { Asset } from "@/api/portal/types";
import { exceedsInlineLimit } from "@/components/renderers/registry";

export type PendingState = "too-large" | "loading" | "failed" | "ready";

/**
 * What, if anything, stands between the viewer and rendering: the asset is past
 * its family's inline limit, its content or media URL has not arrived, or the
 * read that would have produced it failed.
 *
 * "loading" is only ever an answer while a read is in flight. The content read
 * is gated by the registry's contentLoad, which fetches every inline family
 * under the same limit this reads, so an inline asset is either fetched or
 * too large; and a read that ends without a body is "failed", which the
 * viewer shows with the status (#1874). Before, a CSV between the fetch gate's
 * flat 2 MB and its family's 32 MB was neither, and span forever.
 */
export function pendingState({
  asset,
  content,
  fromURL,
  mediaLoading,
  contentFailed = false,
  mediaFailed = false,
}: {
  asset: Pick<Asset, "content_type" | "size_bytes" | "name">;
  content: string | ArrayBuffer | undefined;
  fromURL: boolean;
  mediaLoading: boolean;
  contentFailed?: boolean;
  mediaFailed?: boolean;
}): PendingState {
  if (fromURL) {
    // A URL family renders from the endpoint; a text read beside it (a file
    // under a generic type, read for detection) failing does not stop that.
    if (mediaFailed) return "failed";
    return mediaLoading ? "loading" : "ready";
  }
  if (content !== undefined) {
    return "ready";
  }
  if (contentFailed) {
    return "failed";
  }
  return exceedsInlineLimit(asset.content_type, asset.size_bytes, asset.name) ? "too-large" : "loading";
}
