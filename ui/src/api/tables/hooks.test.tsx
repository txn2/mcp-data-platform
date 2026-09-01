import { describe, it, expect, vi, afterEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useRegisterTable, useScratchTables, useUnregisterTable } from "./hooks";
import type { TableRegistration } from "./types";

const PROBLEM_FILE_CORRECTED = "urn:mcp-data-platform:problem:file-corrected";

// A registration that had to correct the file before it could read it wrote a
// version of that file, and the version panel sits on the same page as the
// answer saying so (#1450). What is asserted here is that the correction
// reaches the queries showing the file: without it the panel keeps showing the
// version before the correction as current, and the record carrying the file's
// size and head stays behind with it.

const REGISTRATION: TableRegistration = {
  id: "reg_1",
  source_kind: "resource",
  source_id: "res-011",
  connection: "acme-scratch",
  catalog: "scratch",
  schema: "uploads",
  table: "analyst_store_list",
  location: "s3://managed-resources/resources/user/david/res-011/v/rev2/",
  columns: [{ name: "store_id", type: "VARCHAR" }],
  registered_by: "alice@example.com",
  registered_at: "2026-08-23T10:00:00Z",
  query_table: "scratch.uploads.analyst_store_list",
  stale: false,
  follow: true,
  repair: true,
};

function stubRegister(body: TableRegistration) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(new Response(JSON.stringify(body), { status: 200 }))),
  );
}

// stubRefusal answers with an RFC 9457 problem, the shape tableFetch turns into
// a TableApiError.
function stubRefusal(status: number, type: string, detail: string) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify({ type, title: "Conflict", status, detail }), { status }),
      ),
    ),
  );
}

// harness renders the hook against a client whose invalidations are recorded,
// so the assertion is on the keys refreshed rather than on a rerender.
function harness(kind: "resource" | "asset", id: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidated: unknown[][] = [];
  vi.spyOn(qc, "invalidateQueries").mockImplementation((filters?: { queryKey?: unknown }) => {
    invalidated.push((filters?.queryKey as unknown[]) ?? []);
    return Promise.resolve();
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  const { result } = renderHook(() => useRegisterTable(kind, id), { wrapper });
  return { result, invalidated };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("registering a stored file as a table", () => {
  it("refreshes a corrected resource's version trail and record", async () => {
    stubRegister({ ...REGISTRATION, repaired: "Saved version 2 of this file." });
    const { result, invalidated } = harness("resource", "res-011");

    result.current.mutate({ connection: "acme-scratch" });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    // ["resources"] is one prefix over both the detail read and the version
    // trail (["resources", id] and ["resources", id, "versions"]).
    expect(invalidated).toContainEqual(["resources"]);
  });

  it("refreshes a corrected asset's versions, content, and record", async () => {
    stubRegister({
      ...REGISTRATION,
      source_kind: "asset",
      source_id: "ast-1",
      repaired: "Saved version 2 of this file.",
    });
    const { result, invalidated } = harness("asset", "ast-1");

    result.current.mutate({ connection: "acme-scratch" });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(invalidated).toContainEqual(["asset-versions", "ast-1"]);
    expect(invalidated).toContainEqual(["asset-content", "ast-1"]);
    expect(invalidated).toContainEqual(["asset", "ast-1"]);
  });

  // The correction is written before the last checks and before the DDL, so a
  // failure can follow one and the file stays changed. The panels showing it
  // are as far behind here as after a success.
  it("refreshes the file when the registration failed after correcting it", async () => {
    stubRefusal(409, PROBLEM_FILE_CORRECTED, "Saved version 2 of this file.");
    const { result, invalidated } = harness("resource", "res-011");

    result.current.mutate({ connection: "acme-scratch", repair: true });
    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(invalidated).toContainEqual(["resources"]);
  });

  it("leaves the file alone when a failure corrected nothing", async () => {
    stubRefusal(500, "about:blank", "the registration could not be completed");
    const { result, invalidated } = harness("resource", "res-011");

    result.current.mutate({ connection: "acme-scratch" });
    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(invalidated).toHaveLength(0);
  });

  it("leaves the file's own queries alone when nothing was corrected", async () => {
    stubRegister(REGISTRATION);
    const { result, invalidated } = harness("resource", "res-011");

    result.current.mutate({ connection: "acme-scratch" });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    // A registration that changed nothing about the file has no reason to make
    // every open resource query refetch. What it does refresh is the two places
    // the new table now appears: the file's own panel, and the cross-source
    // Scratch Tables listing (#1472).
    expect(invalidated).not.toContainEqual(["resources"]);
    expect(invalidated).toEqual([
      ["tables", "resource", "res-011"],
      ["scratch-tables"],
      ["scratch-table"],
    ]);
  });
});

// The cross-source listing (#1472). What is asserted here is the request the
// facets produce -- a facet a reader did not set must not reach the URL, or the
// cache is keyed on a distinction the server does not make -- and that dropping
// a table refreshes the listing it was dropped from.

function stubJSON(body: unknown) {
  const urls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      urls.push(String(input));
      return Promise.resolve(new Response(JSON.stringify(body), { status: 200 }));
    }),
  );
  return urls;
}

function queryWrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe("listing the registered tables", () => {
  it("sends only the facets the reader set", async () => {
    const urls = stubJSON({ data: [], total: 0, page: 1, per_page: 25 });
    const { result } = renderHook(
      () => useScratchTables({ page: 1, perPage: 25, kind: "resource", q: "sales" }),
      { wrapper: queryWrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const url = urls[0] ?? "";
    expect(url.startsWith("/api/v1/tables?")).toBe(true);
    const sent = new URLSearchParams(url.slice(url.indexOf("?")));
    expect(sent.get("per_page")).toBe("25");
    expect(sent.get("kind")).toBe("resource");
    expect(sent.get("q")).toBe("sales");
    // The first page and an unset connection are the server's own defaults, so
    // neither is sent.
    expect(sent.has("page")).toBe(false);
    expect(sent.has("connection")).toBe(false);
  });

  it("asks for the plain listing when the reader set no facet at all", async () => {
    const urls = stubJSON({ data: [], total: 0, page: 1, per_page: 25 });
    const { result } = renderHook(() => useScratchTables({}), { wrapper: queryWrapper() });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(urls[0]).toBe("/api/v1/tables");
  });
});

describe("dropping a registered table", () => {
  it("refreshes the listing it was dropped from as well as the file's own panel", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response(null, { status: 204 }))));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const invalidated: unknown[][] = [];
    vi.spyOn(qc, "invalidateQueries").mockImplementation((filters?: { queryKey?: unknown }) => {
      invalidated.push((filters?.queryKey as unknown[]) ?? []);
      return Promise.resolve();
    });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useUnregisterTable("asset", "ast-008"), { wrapper });

    result.current.mutate("reg_1");
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(invalidated).toEqual([
      ["tables", "asset", "ast-008"],
      ["scratch-tables"],
      ["scratch-table"],
    ]);
  });
});
