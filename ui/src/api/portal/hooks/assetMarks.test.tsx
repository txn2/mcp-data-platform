import { describe, it, expect, vi, afterEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import {
  endOfDayUTC,
  useAssetLinks,
  useAssetMarkHistory,
  useClearAssetStatus,
  useSetAssetStatus,
  useUnverifyAsset,
  useVerifyAsset,
} from "./assetMarks";
import { assetFilterParams } from "./assets";

// Each status and verification change goes to its route and returns the asset
// as it now reads, which replaces the cached copy so the badges update at once
// (#2502, #2503); the links route feeds the frame's link check (#2432).

function harness<T>(hook: () => T, body: unknown = { id: "a1", status: { value: "draft" } }) {
  const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify(body), { status: 200 })));
  vi.stubGlobal("fetch", fetchMock);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(["asset", "a1"], { id: "a1", is_owner: false, share_permission: "editor" });
  const invalidated: unknown[][] = [];
  vi.spyOn(qc, "invalidateQueries").mockImplementation((filters?: { queryKey?: unknown }) => {
    invalidated.push((filters?.queryKey as unknown[]) ?? []);
    return Promise.resolve();
  });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  const { result } = renderHook(hook, { wrapper });
  const call = (i = 0) => {
    const [url, init] = fetchMock.mock.calls[i] as unknown as [string, RequestInit | undefined];
    return { url, method: init?.method ?? "GET", body: init?.body ? JSON.parse(String(init.body)) : undefined };
  };
  return { result, invalidated, call, qc };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("status mutations", () => {
  it("sets a status with its note, and keeps the caller's own fields on the cached asset", async () => {
    const { result, call, invalidated, qc } = harness(() => useSetAssetStatus());
    await result.current.mutateAsync({ id: "a1", status: "deprecated", note: "Replaced by Q4" });
    expect(call()).toEqual({ url: "/api/v1/portal/assets/a1/status", method: "PUT", body: { status: "deprecated", note: "Replaced by Q4" } });
    expect(qc.getQueryData(["asset", "a1"])).toMatchObject({ status: { value: "draft" }, is_owner: false, share_permission: "editor" });
    expect(invalidated).toContainEqual(["assets"]);
    expect(invalidated).toContainEqual(["asset-mark-history", "a1"]);
  });

  it("sends no note when none is given", async () => {
    const { result, call } = harness(() => useSetAssetStatus());
    await result.current.mutateAsync({ id: "a1", status: "published" });
    expect(call().body).toEqual({ status: "published" });
  });

  it("clears a status", async () => {
    const { result, call } = harness(() => useClearAssetStatus());
    await result.current.mutateAsync("a1");
    expect(call()).toMatchObject({ url: "/api/v1/portal/assets/a1/status", method: "DELETE" });
  });
});

describe("verification mutations", () => {
  it("verifies with a note and an expiry", async () => {
    const { result, call } = harness(() => useVerifyAsset());
    await result.current.mutateAsync({ id: "a1", note: "checked", expires_at: "2027-01-31T23:59:59Z" });
    expect(call()).toEqual({
      url: "/api/v1/portal/assets/a1/verification",
      method: "PUT",
      body: { note: "checked", expires_at: "2027-01-31T23:59:59Z" },
    });
  });

  it("verifies with neither, and removes a verification", async () => {
    const { result, call } = harness(() => ({ verify: useVerifyAsset(), unverify: useUnverifyAsset() }));
    await result.current.verify.mutateAsync({ id: "a1", expires_at: null });
    expect(call(0).body).toEqual({});
    await result.current.unverify.mutateAsync("a1");
    expect(call(1)).toMatchObject({ url: "/api/v1/portal/assets/a1/verification", method: "DELETE" });
  });

  it("reads a dialog date as the end of that day in UTC", () => {
    expect(endOfDayUTC("2027-01-31")).toBe("2027-01-31T23:59:59Z");
    expect(endOfDayUTC("")).toBeNull();
    expect(endOfDayUTC("31/01/2027")).toBeNull();
  });
});

describe("reads", () => {
  it("reads the history's events", async () => {
    const events = [{ kind: "status", to: "draft", actor: "a@example.com", at: "2026-11-19T00:00:00Z" }];
    const { result, call } = harness(() => useAssetMarkHistory("a1"), { events });
    await waitFor(() => expect(result.current.data).toEqual(events));
    expect(call().url).toBe("/api/v1/portal/assets/a1/status/history");
  });

  it("reads an asset's links", async () => {
    const links = { links: ["b"], linked_from: [{ id: "c", name: "C", content_type: "text/markdown" }] };
    const { result, call } = harness(() => useAssetLinks("a1"), links);
    await waitFor(() => expect(result.current.data).toEqual(links));
    expect(call().url).toBe("/api/v1/portal/assets/a1/links");
  });
});

describe("listing filters", () => {
  it("sends the status and Verified filters, and leaves them out when unset", () => {
    const sp = new URLSearchParams();
    assetFilterParams({ status: "none", verified: true }, sp);
    expect(sp.get("status")).toBe("none");
    expect(sp.get("verified")).toBe("true");
    const empty = new URLSearchParams();
    assetFilterParams({ verified: false }, empty);
    expect(empty.toString()).toBe("");
  });
});
