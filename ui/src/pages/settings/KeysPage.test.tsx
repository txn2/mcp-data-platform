import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { KeysPage } from "./KeysPage";
import type { APIKeySummary } from "@/api/admin/types";
import { useDeleteAPIKey as realUseDeleteAPIKey } from "@/api/admin/hooks/config";

// The page's hooks are mocked so each server answer can be put in front of the
// rendered page; the delete hook's own behavior is exercised against a stubbed
// fetch below, through the real module.
vi.mock("@/api/admin/hooks", () => ({
  useAPIKeys: vi.fn(),
  useDeleteAPIKey: vi.fn(),
  useSystemInfo: vi.fn(),
  useCreateAPIKey: vi.fn(),
  usePersonas: vi.fn(),
}));

import { useAPIKeys, useDeleteAPIKey, useSystemInfo } from "@/api/admin/hooks";

const mockUseAPIKeys = vi.mocked(useAPIKeys);
const mockUseDeleteAPIKey = vi.mocked(useDeleteAPIKey);
const mockUseSystemInfo = vi.mocked(useSystemInfo);

const refetch = vi.fn();
const deleteMutate = vi.fn();

function listAnswers(state: { keys?: APIKeySummary[]; error?: Error }) {
  mockUseAPIKeys.mockReturnValue({
    data: state.keys ? { keys: state.keys, total: state.keys.length } : undefined,
    isLoading: false,
    isError: Boolean(state.error),
    error: state.error ?? null,
    refetch,
  } as unknown as ReturnType<typeof useAPIKeys>);
}

beforeEach(() => {
  vi.clearAllMocks();
  mockUseSystemInfo.mockReturnValue({ data: { config_mode: "database" } } as unknown as ReturnType<typeof useSystemInfo>);
  mockUseDeleteAPIKey.mockReturnValue({ mutate: deleteMutate, isPending: false } as unknown as ReturnType<typeof useDeleteAPIKey>);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// A listing the server could not answer is not an empty listing (#1715): the
// key routes read the key store first and answer 500 when it cannot be read.
describe("KeysPage: the listing fails", () => {
  it("says the keys could not be loaded instead of saying there are none", () => {
    listAnswers({ error: new Error("failed to read api keys") });
    render(<KeysPage />);

    expect(screen.getByText("Failed to load API keys: failed to read api keys")).toBeInTheDocument();
    expect(screen.queryByText("No API keys configured")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /Retry/ }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it("still says there are none when the listing is empty", () => {
    listAnswers({ keys: [] });
    render(<KeysPage />);

    expect(screen.getByText("No API keys configured")).toBeInTheDocument();
    expect(screen.queryByText(/Failed to load API keys/)).toBeNull();
  });
});

// A key another admin or replica already deleted answers 404 when its stale row
// is deleted; the page says so rather than leaving the confirm open silently.
describe("KeysPage: a delete is refused", () => {
  it("names the key and the reason, and closes the confirm", () => {
    listAnswers({ keys: [{ name: "ci", roles: ["admin"], source: "database" }] });
    deleteMutate.mockImplementation((_name: string, opts: { onError: (err: Error) => void }) => {
      opts.onError(new Error("key not found"));
    });
    render(<KeysPage />);

    fireEvent.click(screen.getByRole("button", { name: /Delete/ }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));

    expect(deleteMutate).toHaveBeenCalledWith("ci", expect.anything());
    expect(screen.getByText('Could not delete "ci": key not found')).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Confirm" })).toBeNull();
  });

  it("shows nothing when the delete succeeds", () => {
    listAnswers({ keys: [{ name: "ci", roles: ["admin"], source: "database" }] });
    deleteMutate.mockImplementation((_name: string, opts: { onSuccess: () => void }) => {
      opts.onSuccess();
    });
    render(<KeysPage />);

    fireEvent.click(screen.getByRole("button", { name: /Delete/ }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));

    expect(screen.queryByText(/Could not delete/)).toBeNull();
    expect(screen.queryByRole("button", { name: "Confirm" })).toBeNull();
  });
});

describe("useDeleteAPIKey", () => {
  function wrapperFor(qc: QueryClient) {
    return function Wrapper({ children }: { children: ReactNode }) {
      return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
    };
  }

  it("reports the server's reason and refreshes the listing when the delete is refused", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ status: 404, detail: "key not found" }), {
          status: 404,
          headers: { "Content-Type": "application/problem+json" },
        }),
      ),
    );
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    const { result } = renderHook(() => realUseDeleteAPIKey(), { wrapper: wrapperFor(qc) });

    result.current.mutate("already gone");

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error?.message).toBe("key not found");
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["auth", "keys"] });
    expect(vi.mocked(fetch).mock.calls[0]?.[0]).toBe("/api/v1/admin/auth/keys/already%20gone");
  });

  it("refreshes the listing when the delete succeeds", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: "deleted" }), { status: 200 })),
    );
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    const { result } = renderHook(() => realUseDeleteAPIKey(), { wrapper: wrapperFor(qc) });

    result.current.mutate("ci");

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["auth", "keys"] });
  });
});
