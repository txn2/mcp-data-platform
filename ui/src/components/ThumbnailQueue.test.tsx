import { describe, it, expect, vi, beforeEach } from "vitest";
import { useEffect } from "react";
import { render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Asset } from "@/api/portal/types";

// apiFetchRaw is used to fetch each asset's content before capture.
const { fetchRaw, captureMode } = vi.hoisted(() => ({
  fetchRaw: vi.fn(),
  captureMode: { value: "captured" as "captured" | "failed" },
}));
vi.mock("@/api/portal/client", () => ({ apiFetchRaw: fetchRaw }));

// Stand in for the real capturer (html2canvas is browser-only). Reports a
// captured or failed result per asset as soon as it renders. Keying the effect
// on assetId (not the callback) makes it fire once per asset regardless of how
// the queue mounts/unmounts the generator between items.
vi.mock("./ThumbnailGenerator", () => ({
  ThumbnailGenerator: ({
    assetId,
    onCaptured,
    onFailed,
  }: {
    assetId: string;
    onCaptured?: () => void;
    onFailed?: () => void;
  }) => {
    useEffect(() => {
      if (captureMode.value === "failed") onFailed?.();
      else onCaptured?.();
    }, [assetId, onCaptured, onFailed]);
    return null;
  },
}));

import { ThumbnailQueue } from "./ThumbnailQueue";

function asset(id: string, over: Partial<Asset> = {}): Asset {
  return {
    id,
    owner_id: "u1",
    owner_email: "u1@example.com",
    name: id,
    content_type: "text/html",
    s3_bucket: "b",
    s3_key: `k/${id}`,
    thumbnail_s3_key: "", // missing -> needs a thumbnail
    size_bytes: 1,
    tags: [],
    provenance: {} as Asset["provenance"],
    session_id: "",
    current_version: 1,
    created_at: "",
    updated_at: "",
    ...over,
  };
}

describe("ThumbnailQueue invalidation batching", () => {
  beforeEach(() => {
    fetchRaw.mockReset();
    fetchRaw.mockResolvedValue({ ok: true, text: () => Promise.resolve("<html></html>") });
    captureMode.value = "captured";
  });

  it("invalidates the asset list once after a batch of captures, not per capture", async () => {
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries").mockResolvedValue();

    const assets = [asset("a"), asset("b"), asset("c")];

    render(
      <QueryClientProvider client={qc}>
        <ThumbnailQueue assets={assets} />
      </QueryClientProvider>,
    );

    // All three assets get fetched + captured.
    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(3));

    // The asset list is invalidated exactly once for the whole drain, not once
    // per capture (which is the storm this fix removes).
    await waitFor(() =>
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ["assets"] }),
    );
    expect(invalidate).toHaveBeenCalledTimes(1);
  });

  it("re-queues an asset whose thumbnail was written under the legacy name", async () => {
    const qc = new QueryClient();
    const assets = [
      // Already thumbnailed under the current hidden names: nothing to do.
      asset("hidden", {
        content_type: "text/csv",
        thumbnail_s3_key: "k/hidden/.thumbnail.png",
        thumbnail_dark_s3_key: "k/hidden/.thumbnail_dark.png",
      }),
      // Thumbnailed before the rename. The objects sit beside the content as
      // ordinary files, which is what keeps the CSV from registering as a
      // table, so this one is captured again.
      asset("legacy", {
        content_type: "text/csv",
        thumbnail_s3_key: "k/legacy/thumbnail.png",
        thumbnail_dark_s3_key: "k/legacy/thumbnail_dark.png",
      }),
    ];

    render(
      <QueryClientProvider client={qc}>
        <ThumbnailQueue assets={assets} />
      </QueryClientProvider>,
    );

    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(1));
    expect(fetchRaw).toHaveBeenCalledWith("/assets/legacy/content");
  });

  it("does not invalidate when every capture fails (nothing to refresh)", async () => {
    captureMode.value = "failed";
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries").mockResolvedValue();

    const assets = [asset("a"), asset("b"), asset("c")];

    render(
      <QueryClientProvider client={qc}>
        <ThumbnailQueue assets={assets} />
      </QueryClientProvider>,
    );

    // All three are processed (fetched) but report failure, so the queue drains
    // with dirtyRef never set and must not trigger a refetch.
    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(3));
    // Let the drain effect run after the final advance.
    await waitFor(() => expect(invalidate).not.toHaveBeenCalled());
    expect(invalidate).not.toHaveBeenCalled();
  });
});

// Capture is a long main-thread task per asset. These are the bounds that keep
// the assets home from spending a first visit on it (#1351).
describe("ThumbnailQueue bounds", () => {
  beforeEach(() => {
    fetchRaw.mockReset();
    fetchRaw.mockResolvedValue({ ok: true, text: () => Promise.resolve("<html></html>") });
    captureMode.value = "captured";
  });

  it("captures at most eight assets per visit", async () => {
    const qc = new QueryClient();
    vi.spyOn(qc, "invalidateQueries").mockResolvedValue();

    const assets = Array.from({ length: 20 }, (_, i) => asset(`a${i}`));

    render(
      <QueryClientProvider client={qc}>
        <ThumbnailQueue assets={assets} />
      </QueryClientProvider>,
    );

    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(8));
    // Give the queue room to overrun the cap if it were going to.
    await new Promise((r) => setTimeout(r, 50));
    expect(fetchRaw).toHaveBeenCalledTimes(8);
  });

  it("skips an asset too large to render twice cheaply", async () => {
    const qc = new QueryClient();
    vi.spyOn(qc, "invalidateQueries").mockResolvedValue();

    render(
      <QueryClientProvider client={qc}>
        <ThumbnailQueue
          assets={[asset("huge", { size_bytes: 5 * 1024 * 1024 }), asset("small")]}
        />
      </QueryClientProvider>,
    );

    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(1));
    expect(fetchRaw).toHaveBeenCalledWith("/assets/small/content");
  });

  // A hidden tab is where a capture is least useful and most disruptive: it
  // lands as jank the moment the reader comes back.
  it("captures nothing while the tab is hidden", async () => {
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      get: () => "hidden" as DocumentVisibilityState,
    });
    try {
      const qc = new QueryClient();
      vi.spyOn(qc, "invalidateQueries").mockResolvedValue();

      render(
        <QueryClientProvider client={qc}>
          <ThumbnailQueue assets={[asset("a")]} />
        </QueryClientProvider>,
      );

      await new Promise((r) => setTimeout(r, 50));
      expect(fetchRaw).not.toHaveBeenCalled();
    } finally {
      Object.defineProperty(document, "visibilityState", {
        configurable: true,
        get: () => "visible" as DocumentVisibilityState,
      });
    }
  });
});
