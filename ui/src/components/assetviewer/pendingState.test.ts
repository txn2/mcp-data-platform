import { describe, it, expect } from "vitest";
import {
  contentLoad,
  REGISTERED_CONTENT_TYPES,
  rendersFromURL,
  resolveRenderer,
  TEXT_INLINE_LIMIT,
} from "@/components/renderers/registry";
import { pendingState } from "./pendingState";

// Every family the registry names, the prefix families by a representative,
// and the fallbacks a type string can land in.
const FAMILIES = [
  ...REGISTERED_CONTENT_TYPES,
  "image/png",
  "audio/mpeg",
  "video/mp4",
  "application/vnd.acme.report+json",
  "application/vnd.acme.report+xml",
  "text/x-unknown",
  "application/octet-stream",
];

/** The size the family's cutoff sits at: its inline limit, else the text limit. */
function limitOf(contentType: string): number {
  return resolveRenderer({ contentType }).inlineLimit ?? TEXT_INLINE_LIMIT;
}

describe("pendingState", () => {
  // The criterion of #1874: no asset can reach a state where its content is
  // not requested and the view is "loading". The fetch gate and the view read
  // one rule, so for every family, just under and just over its limit, the
  // viewer either requests the content or shows something other than loading.
  it.each(FAMILIES.flatMap((ct) => [[ct, limitOf(ct) - 1], [ct, limitOf(ct) + 1]] as const))(
    "never leaves %s at %d bytes unrequested and loading",
    (contentType, sizeBytes) => {
      const asset = { content_type: contentType, size_bytes: sizeBytes, name: "file" };
      const requested = contentLoad(contentType, sizeBytes, "file") === "fetch";
      const state = pendingState({
        asset,
        content: undefined,
        fromURL: rendersFromURL(contentType, "file"),
        mediaLoading: false,
      });
      expect(requested || state !== "loading").toBe(true);
    },
  );

  it("shows a failed read as failed, not as loading", () => {
    const asset = { content_type: "text/csv", size_bytes: 5 * 1024 * 1024, name: "d.csv" };
    expect(pendingState({ asset, content: undefined, fromURL: false, mediaLoading: false, contentFailed: true })).toBe(
      "failed",
    );
    expect(pendingState({ asset, content: undefined, fromURL: false, mediaLoading: false })).toBe("loading");
    expect(pendingState({ asset, content: "a,b", fromURL: false, mediaLoading: false, contentFailed: true })).toBe(
      "ready",
    );
  });

  it("shows a failed media read as failed, and ignores a failed detection read beside it", () => {
    const asset = { content_type: "image/png", size_bytes: 10, name: "p.png" };
    expect(pendingState({ asset, content: undefined, fromURL: true, mediaLoading: false, mediaFailed: true })).toBe(
      "failed",
    );
    expect(pendingState({ asset, content: undefined, fromURL: true, mediaLoading: false, contentFailed: true })).toBe(
      "ready",
    );
    expect(pendingState({ asset, content: undefined, fromURL: true, mediaLoading: true })).toBe("loading");
  });
});
