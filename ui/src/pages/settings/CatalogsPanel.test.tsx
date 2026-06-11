import { describe, it, expect, vi } from "vitest";
import { act, render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const h = vi.hoisted(() => ({
  retryMutate: vi.fn(),
  refreshMutate: vi.fn(),
}));

// Override only the hooks SpecsManager consumes; keep everything else real.
vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useManualRetryEmbedding: () => ({ mutate: h.retryMutate, isPending: false }),
    useRefreshAPICatalogSpec: () => ({ mutate: h.refreshMutate, isPending: false }),
    useDeleteAPICatalogSpec: () => ({ mutate: vi.fn(), isPending: false }),
    useAPICatalogEmbeddingHealth: () => ({ data: undefined }),
    useAPICatalogEmbeddingStatuses: () => ({
      data: { specs: [{ spec_name: "petstore", job_status: "failed" }] },
    }),
  };
});

// The spec list is fetched with a direct apiFetch in a useEffect; stub it.
vi.mock("@/api/admin/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/client")>();
  return {
    ...actual,
    apiFetch: vi.fn((url: string) =>
      url.includes("/specs")
        ? Promise.resolve({ specs: [{ spec_name: "petstore", source_kind: "inline" }] })
        : Promise.resolve({}),
    ),
  };
});

import { SourceBadge, SpecsManager } from "./CatalogsPanel";

describe("SourceBadge", () => {
  it("renders a label and icon for every known source kind", () => {
    const cases: Array<{ kind: "inline" | "upload" | "url" | "embedded"; label: string }> = [
      { kind: "inline", label: "inline" },
      { kind: "upload", label: "upload" },
      { kind: "url", label: "URL" },
      { kind: "embedded", label: "embedded" },
    ];
    for (const { kind, label } of cases) {
      const { unmount } = render(<SourceBadge kind={kind} />);
      expect(screen.getByText(label)).toBeInTheDocument();
      unmount();
    }
  });

  // Regression: an embedded spec (re-seeded from the platform self-connection)
  // previously crashed the whole catalogs page because SourceBadge indexed a
  // three-key map and read `.icon` off undefined. It must render, not throw.
  it("does not throw for embedded specs", () => {
    expect(() => render(<SourceBadge kind="embedded" />)).not.toThrow();
  });

  // Any future backend source_kind must degrade to a plain badge showing the
  // raw kind rather than crashing the page.
  it("degrades gracefully for an unknown source kind", () => {
    const kind = "future-kind" as unknown as "inline";
    expect(() => render(<SourceBadge kind={kind} />)).not.toThrow();
    expect(screen.getByText("future-kind")).toBeInTheDocument();
  });
});

describe("SpecsManager retry debounce", () => {
  function renderManager() {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
      <QueryClientProvider client={qc}>
        <SpecsManager catalogID="cat1" isReadOnly={false} />
      </QueryClientProvider>,
    );
  }

  // Regression: a slow re-embed gave no feedback, so operators clicked Retry
  // repeatedly and queued duplicate jobs against the same spec. Two clicks in
  // the SAME tick (before React commits the disabled state) must be stopped by
  // the synchronous inFlightRetryRef guard, not just by the disabled attribute.
  // Both raw clicks are dispatched inside one act() so no re-render (and so no
  // disabled commit) happens between them; only the ref guard can block the
  // second. The mutate mock never settles, so the row stays busy.
  it("the synchronous ref guard collapses a same-tick double click to one mutation", async () => {
    h.retryMutate.mockClear();
    renderManager();

    const retry = await screen.findByRole("button", { name: /^retry$/i });
    act(() => {
      retry.click();
      retry.click();
    });

    expect(h.retryMutate).toHaveBeenCalledTimes(1);
    expect(await screen.findByRole("button", { name: /retrying/i })).toBeDisabled();
  });

  // Once the mutation settles, onSettled must clear the in-flight entry so the
  // button re-enables and a later click can fire a fresh mutation. Without this
  // the row would be stuck disabled forever.
  it("re-enables the button after the mutation settles", async () => {
    h.retryMutate.mockReset();
    // First call resolves immediately via its onSettled; capture later calls too.
    h.retryMutate.mockImplementation((_vars, opts?: { onSettled?: () => void }) => {
      opts?.onSettled?.();
    });
    renderManager();

    const retry = await screen.findByRole("button", { name: /^retry$/i });
    fireEvent.click(retry);

    // onSettled ran synchronously, so the row is enabled again as "Retry".
    const reenabled = await screen.findByRole("button", { name: /^retry$/i });
    expect(reenabled).not.toBeDisabled();

    fireEvent.click(reenabled);
    expect(h.retryMutate).toHaveBeenCalledTimes(2);
  });
});
