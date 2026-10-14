import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/api/portal/catalog", () => ({
  useDataset: vi.fn(),
  useDefinition: vi.fn(),
}));

import { CatalogNodeDetail } from "./CatalogNodeDetail";
import { useDataset, useDefinition } from "@/api/portal/catalog";
import { ApiError } from "@/api/portal/client";

const ref = "mcp:dataset:(trino,acme,analytics.public.daily_sales)";

const result = (over: Record<string, unknown>) =>
  ({ data: undefined, isLoading: false, isError: false, error: null, ...over }) as never;

beforeEach(() => {
  vi.mocked(useDataset).mockReturnValue(result({}));
  vi.mocked(useDefinition).mockReturnValue(result({}));
});

// The catalog is what settles whether it holds an entity (#1610). This
// component used to read the answer off the record's own fields, which called a
// dataset nobody had documented missing.
describe("CatalogNodeDetail", () => {
  it("says the catalog does not have the dataset when the lookup is a 404", () => {
    vi.mocked(useDataset).mockReturnValue(
      result({ isError: true, error: new ApiError(404, "dataset not found") }),
    );
    render(<CatalogNodeDetail urn={ref} />);
    expect(screen.getByText(/the catalog does not have it/)).toBeInTheDocument();
  });

  it("reports a catalog it could not read as a failure rather than as an absence", () => {
    vi.mocked(useDataset).mockReturnValue(
      result({ isError: true, error: new ApiError(500, "reading the catalog failed") }),
    );
    render(<CatalogNodeDetail urn={ref} />);
    expect(screen.getByText(/Could not read the catalog/)).toBeInTheDocument();
    expect(screen.queryByText(/does not have it/)).not.toBeInTheDocument();
  });

  it("shows a dataset the catalog holds and nobody has documented as held, not missing", () => {
    vi.mocked(useDataset).mockReturnValue(result({ data: { reference: ref, aspects: {} } }));
    render(<CatalogNodeDetail urn={ref} />);
    expect(screen.getByText(/In the catalog/)).toBeInTheDocument();
    expect(screen.getByText(/with no description, owners or tags recorded/)).toBeInTheDocument();
  });

  it("renders what the catalog holds for a documented dataset, its domain by name", () => {
    vi.mocked(useDataset).mockReturnValue(
      result({
        data: {
          reference: ref,
          aspects: { description: "One row per order.", tags: ["finance"], domain: "mcp:domain:d1" },
          names: { "mcp:domain:d1": "Sales" },
        },
      }),
    );
    render(<CatalogNodeDetail urn={ref} />);
    expect(screen.getByText("One row per order.")).toBeInTheDocument();
    expect(screen.getByText("finance")).toBeInTheDocument();
    expect(screen.getByText("Domain: Sales")).toBeInTheDocument();
  });

  it("reads a column on its dataset and a definition by its reference", () => {
    render(<CatalogNodeDetail urn="mcp:column:(trino,acme,analytics.public.daily_sales,amount)" />);
    expect(useDataset).toHaveBeenLastCalledWith(ref);
    vi.mocked(useDefinition).mockReturnValue(
      result({ data: { reference: "mcp:glossary_term:revenue", aspects: { description: "Money in." } } }),
    );
    render(<CatalogNodeDetail urn="mcp:glossary_term:revenue" />);
    expect(useDefinition).toHaveBeenLastCalledWith("mcp:glossary_term:revenue");
    expect(screen.getByText("Money in.")).toBeInTheDocument();
  });
});
