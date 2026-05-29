import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ApiError } from "@/api/admin/client";
import { promInstantFor, promRangeFor } from "@/mocks/data/observability";

// Mock the observability hooks so the view renders against canned
// Prometheus data (keyed off the PromQL the real builders produce),
// exercising the builders + adapters + drilldown logic without fetch.
vi.mock("@/api/observability/hooks", async () => {
  const actual = await vi.importActual<typeof import("@/api/observability/hooks")>(
    "@/api/observability/hooks",
  );
  return {
    ...actual,
    useObservabilityQuery: vi.fn((query: string) => ({
      data: promInstantFor(query),
      isLoading: false,
      error: null,
    })),
    useObservabilityQueryRange: vi.fn((q: string, start: number, end: number, step: number) => ({
      data: promRangeFor(q, start, end, step),
      isLoading: false,
      error: null,
    })),
  };
});

import { useObservabilityQuery } from "@/api/observability/hooks";
import { APIGatewayView, RATE_SERIES } from "./APIGatewayView";
import { promMatrixToTimeseries } from "./promql";
import { promRangeFor as promRange } from "@/mocks/data/observability";

const mockQuery = vi.mocked(useObservabilityQuery);

beforeEach(() => {
  mockQuery.mockImplementation((query: string) => ({
    data: promInstantFor(query),
    isLoading: false,
    error: null,
  }) as unknown as ReturnType<typeof useObservabilityQuery>);
});

describe("request-rate series wiring", () => {
  // Regression guard for the "flat line" bug: the rate chart only
  // renders if its series dataKey is a field promMatrixToTimeseries
  // actually populates with the value. A mismatch (e.g. value in
  // `count` but chart plotting `success_count`) draws a zero line.
  it("plots a field the matrix adapter populates with a non-zero value", () => {
    const start = 1_700_000_000;
    const out = promMatrixToTimeseries(promRange("rate", start, start + 240, 60));
    expect(out.length).toBeGreaterThan(0);
    // The chart's series dataKey must be a field the adapter fills with
    // real values; otherwise the line is flat regardless of traffic. This
    // fails if RATE_SERIES.dataKey is reverted to e.g. "success_count".
    const key = RATE_SERIES[0]!.dataKey;
    expect(out.some((b) => Number(b[key]) > 0)).toBe(true);
  });
});

// rowButton finds the clickable breakdown row for a dimension value.
// Connection/operation names also appear as SVG labels in the sankey, so a
// plain getByText is ambiguous at the root level; the list rows are the
// only <button>s containing the name (preset/breadcrumb buttons differ).
function rowButton(name: string): HTMLElement {
  const btn = screen
    .getAllByRole("button")
    .find(
      (b) =>
        (b.textContent || "").includes(name) &&
        !["1h", "6h", "24h", "7d", "Connections"].includes((b.textContent || "").trim()),
    );
  if (!btn) throw new Error(`no clickable row for "${name}"`);
  return btn;
}

describe("APIGatewayView", () => {
  it("renders top connections at the root level", () => {
    render(<APIGatewayView />);
    expect(screen.getByText("Top connections by request volume")).toBeInTheDocument();
    expect(rowButton("salesforce")).toBeInTheDocument();
    expect(rowButton("stripe")).toBeInTheDocument();
  });

  it("drills into a connection: stat cards + top endpoints", () => {
    render(<APIGatewayView />);
    fireEvent.click(rowButton("salesforce"));

    // Breadcrumb shows the selected connection.
    expect(screen.getByRole("button", { name: "salesforce" })).toBeInTheDocument();
    // Latency / volume stat cards (from firstScalar over the mock vectors).
    expect(screen.getByText("Total requests")).toBeInTheDocument();
    expect(screen.getByText("Error rate")).toBeInTheDocument();
    expect(screen.getByText("p95")).toBeInTheDocument();
    // Top endpoints for the connection.
    expect(screen.getByText("Top endpoints by request volume")).toBeInTheDocument();
    expect(screen.getByText("listContacts")).toBeInTheDocument();
  });

  it("drills into an endpoint: status/method/identity breakdowns", () => {
    render(<APIGatewayView />);
    fireEvent.click(rowButton("salesforce"));
    fireEvent.click(screen.getByText("listContacts"));

    expect(screen.getByText("Status class")).toBeInTheDocument();
    expect(screen.getByText("Method")).toBeInTheDocument();
    expect(screen.getByText("Identity")).toBeInTheDocument();
    // Breadcrumb shows the full path.
    expect(screen.getByText("listContacts")).toBeInTheDocument();
  });

  it("renders the empty state when the proxy returns 503", () => {
    mockQuery.mockImplementation((query: string) => {
      // Top-connections query errors with 503; the view should short-circuit
      // to the empty state regardless of other queries.
      if (query.includes("by (connection)")) {
        return {
          data: undefined,
          isLoading: false,
          error: new ApiError(503, "observability backend not configured"),
        } as unknown as ReturnType<typeof useObservabilityQuery>;
      }
      return { data: undefined, isLoading: false, error: null } as unknown as ReturnType<
        typeof useObservabilityQuery
      >;
    });

    render(<APIGatewayView />);
    expect(screen.getByText("API gateway metrics unavailable")).toBeInTheDocument();
    expect(screen.queryByText("Top connections by request volume")).not.toBeInTheDocument();
  });
});
