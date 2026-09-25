import { describe, it, expect, vi, afterEach } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { useAuthStore } from "@/stores/auth";
import { ContentFetchError } from "./contentFetch";
import { useContentUrl } from "./useContentUrl";

describe("useContentUrl", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    useAuthStore.setState({ authMethod: null, apiKey: "" });
  });

  it("hands a cookie session the endpoint unchanged", () => {
    useAuthStore.setState({ authMethod: "cookie" });
    const { result } = renderHook(() => useContentUrl("/c"));
    expect(result.current).toMatchObject({ src: "/c", loading: false, error: null });
  });

  // #1874: an API-key session's read that fails names its status, and Retry
  // reads it again rather than leaving the viewer on the failure.
  it("reports a failed read's status and reads again on retry", async () => {
    useAuthStore.setState({ authMethod: "apikey", apiKey: "k" });
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: () => "blob:x", revokeObjectURL: () => {} }));
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response("", { status: 404 }))
      .mockResolvedValueOnce(new Response("png", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(() => useContentUrl("/c"));
    await vi.waitFor(() => expect(result.current.error).toBeInstanceOf(ContentFetchError));
    expect(result.current.error?.status).toBe(404);

    act(() => result.current.retry());
    await vi.waitFor(() => expect(result.current.src).toBe("blob:x"));
    expect(result.current.error).toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
