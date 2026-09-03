import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/api/portal/datahub", () => ({
  useCatalogEntity: vi.fn(),
  useDataHubConnections: vi.fn(),
}));

import { CatalogNodeDetail } from "./CatalogNodeDetail";
import { useCatalogEntity, useDataHubConnections } from "@/api/portal/datahub";
import { ApiError } from "@/api/portal/client";

const urn = "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.public.daily_sales,PROD)";

const result = (over: Record<string, unknown>) =>
  ({ data: undefined, isLoading: false, isError: false, error: null, ...over }) as never;

beforeEach(() => {
  vi.mocked(useDataHubConnections).mockReturnValue({ data: [{ name: "acme" }] } as never);
});

// The catalog is what settles whether it holds an entity (#1610). This
// component used to read the answer off the record's own fields, which called a
// dataset nobody had documented missing.
describe("CatalogNodeDetail", () => {
  it("says the catalog does not have the dataset when the lookup is a 404", () => {
    vi.mocked(useCatalogEntity).mockReturnValue(
      result({ isError: true, error: new ApiError(404, "datahub holds no entity") }),
    );
    render(<CatalogNodeDetail urn={urn} />);
    expect(screen.getByText(/Not found in/)).toBeInTheDocument();
    expect(screen.getByText(/the catalog does not have it/)).toBeInTheDocument();
  });

  it("reports a catalog it could not reach as a failure rather than as an absence", () => {
    vi.mocked(useCatalogEntity).mockReturnValue(
      result({ isError: true, error: new ApiError(502, "entity read failed") }),
    );
    render(<CatalogNodeDetail urn={urn} />);
    expect(screen.getByText(/Could not reach the acme catalog/)).toBeInTheDocument();
    expect(screen.queryByText(/Not found in/)).not.toBeInTheDocument();
  });

  it("shows a dataset the catalog holds and nobody has documented as held, not missing", () => {
    vi.mocked(useCatalogEntity).mockReturnValue(result({ data: { urn, context: { urn } } }));
    render(<CatalogNodeDetail urn={urn} />);
    expect(screen.getByText(/In acme/)).toBeInTheDocument();
    expect(
      screen.getByText(/holds this dataset with no description, owners or tags recorded/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Not found in/)).not.toBeInTheDocument();
  });

  it("renders what the catalog holds for a documented dataset", () => {
    vi.mocked(useCatalogEntity).mockReturnValue(
      result({
        data: {
          urn,
          context: { urn, description: "One row per order.", tags: ["finance"] },
        },
      }),
    );
    render(<CatalogNodeDetail urn={urn} />);
    expect(screen.getByText("One row per order.")).toBeInTheDocument();
    expect(screen.getByText("finance")).toBeInTheDocument();
  });
});
