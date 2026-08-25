import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook } from "@testing-library/react";
import type { UseQueryResult } from "@tanstack/react-query";
import { useAPICatalogs } from "@/api/admin/hooks";
import {
  useAPIConnections,
  useAPIOperation,
  useAPIOperations,
  useCatalogOperations,
  useCatalogSpecOperation,
  useCatalogSpecs,
} from "@/api/apis/hooks";
import { useBrowseDetail, useBrowseIndex, useBrowseSources } from "./useApiBrowse";

// What the scope decides (#1478). These hold that the page asks each scope's
// own routes and only those: a portal reader never asks the admin API a
// question it would answer with 403, and an operator's catalog view never asks
// for a connection that may not exist.

vi.mock("@/api/apis/hooks");
vi.mock("@/api/admin/hooks");

/** query fakes one react-query result with the fields these hooks read. */
function query<T>(data: T, over: Partial<UseQueryResult<T>> = {}): UseQueryResult<T> {
  return { data, isLoading: false, error: null, ...over } as UseQueryResult<T>;
}

/** pending is a query that has answered nothing yet, for whatever it is mocking.
 * The cast is what lets one helper stand in for every hook's result type. */
function pending<T>(over: Partial<UseQueryResult<T>> = {}): UseQueryResult<T> {
  return { data: undefined, isLoading: false, error: null, ...over } as unknown as UseQueryResult<T>;
}

beforeEach(() => {
  vi.mocked(useAPIConnections).mockReturnValue(query({ connections: [] }));
  vi.mocked(useAPICatalogs).mockReturnValue(query([]));
  vi.mocked(useAPIOperations).mockReturnValue(pending());
  vi.mocked(useCatalogSpecs).mockReturnValue(pending());
  vi.mocked(useCatalogSpecOperation).mockReturnValue(pending());
  vi.mocked(useAPIOperation).mockReturnValue(pending());
  vi.mocked(useCatalogOperations).mockReturnValue({
    operations: [],
    failedSpecs: [],
    isLoading: false,
  });
});

describe("the browser's sources", () => {
  it("labels a connection by how much of it the reader reaches", () => {
    vi.mocked(useAPIConnections).mockReturnValue(
      query({
        connections: [
          {
            name: "acme-billing",
            operation_count: 1,
            specs: [],
          },
        ],
      }),
    );

    const { result } = renderHook(() => useBrowseSources("portal"));

    expect(result.current.options).toEqual([
      { value: "acme-billing", label: "acme-billing", hint: "1 operation" },
    ]);
  });

  it("labels a catalog by how many specs it holds", () => {
    vi.mocked(useAPICatalogs).mockReturnValue(
      query([
        {
          id: "stripe-api",
          name: "stripe",
          display_name: "Stripe API",
          spec_count: 2,
          ref_count: 1,
        },
      ]),
    );

    const { result } = renderHook(() => useBrowseSources("admin"));

    expect(result.current.options).toEqual([
      { value: "stripe-api", label: "Stripe API", hint: "2 specs" },
    ]);
  });

  it("does not ask the caller-scoped route for an operator's listing", () => {
    renderHook(() => useBrowseSources("admin"));

    expect(useAPIConnections).toHaveBeenCalledWith(false);
    expect(useAPICatalogs).toHaveBeenCalledWith(true);
  });

  // The catalog route answers 403 to a non-administrator, and a page that
  // serves both audiences must not spend a rejected request per mount to
  // discover that.
  it("does not ask the admin route for a caller's listing", () => {
    renderHook(() => useBrowseSources("portal"));

    expect(useAPICatalogs).toHaveBeenCalledWith(false);
    expect(useAPIConnections).toHaveBeenCalledWith(true);
  });
});

describe("the browser's index", () => {
  it("reads one connection's operations, with the connection alongside them", () => {
    const connection = { name: "acme-billing", operation_count: 1, specs: [] };
    vi.mocked(useAPIOperations).mockReturnValue(
      query({
        connection,
        operations: [{ operation_id: "listCustomers", method: "GET", path: "/v1/customers" }],
      }),
    );

    const { result } = renderHook(() => useBrowseIndex("portal", "acme-billing"));

    expect(result.current.operations).toHaveLength(1);
    expect(result.current.connection).toEqual(connection);
    expect(result.current.failedSpecs).toEqual([]);
  });

  it("reads a catalog spec by spec, and has no connection to call", () => {
    vi.mocked(useCatalogSpecs).mockReturnValue(
      query({ specs: [{ spec_name: "core", source_kind: "inline" as const }] }),
    );
    vi.mocked(useCatalogOperations).mockReturnValue({
      operations: [
        { operation_id: "listCustomers", method: "GET", path: "/v1/customers", spec: "core" },
      ],
      failedSpecs: [{ name: "billing", unparseable: true }],
      isLoading: false,
    });

    const { result } = renderHook(() => useBrowseIndex("admin", "stripe-api"));

    expect(result.current.operations).toHaveLength(1);
    expect(result.current.connection).toBeUndefined();
    expect(result.current.failedSpecs).toEqual([{ name: "billing", unparseable: true }]);
    // The connection-scoped read is disabled rather than asked for a catalog id.
    expect(useAPIOperations).toHaveBeenCalledWith(undefined);
  });
});

describe("the browser's detail", () => {
  it("asks the connection route in the portal scope, and not the catalog one", () => {
    renderHook(() =>
      useBrowseDetail("portal", "acme-billing", { operationID: "listCustomers", spec: "core" }),
    );

    expect(useAPIOperation).toHaveBeenCalledWith("acme-billing", "listCustomers", "core");
    expect(useCatalogSpecOperation).toHaveBeenCalledWith(undefined, undefined, undefined);
  });

  it("asks the catalog route in the admin scope, and not the connection one", () => {
    renderHook(() =>
      useBrowseDetail("admin", "stripe-api", { operationID: "listCustomers", spec: "core" }),
    );

    expect(useCatalogSpecOperation).toHaveBeenCalledWith("stripe-api", "core", "listCustomers");
    expect(useAPIOperation).toHaveBeenCalledWith(undefined, undefined, undefined);
  });

  it("asks for nothing until an operation is selected", () => {
    renderHook(() => useBrowseDetail("portal", "acme-billing", null));

    expect(useAPIOperation).toHaveBeenCalledWith(undefined, undefined, undefined);
  });

  it("carries a refusal through as the message the reader is shown", () => {
    vi.mocked(useAPIOperation).mockReturnValue(
      pending({ error: new Error("operation not found") }),
    );

    const { result } = renderHook(() =>
      useBrowseDetail("portal", "acme-billing", { operationID: "gone", spec: "core" }),
    );

    expect(result.current.error).toBe("operation not found");
  });
});
