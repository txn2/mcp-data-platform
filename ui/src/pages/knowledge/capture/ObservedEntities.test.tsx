import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ObservedEntity } from "@/api/admin/types";
import { ObservedEntities } from "./ObservedEntities";

const urn = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.orders,PROD)";

function entity(over: Partial<ObservedEntity> = {}): ObservedEntity {
  return {
    urn,
    query_table: "iceberg.retail.orders",
    connection: "primary",
    estimated_rows: 1200,
    ...over,
  };
}

describe("ObservedEntities", () => {
  it("renders nothing when the server observed nothing", () => {
    // The absent block is the whole degrade path: an unresolvable entity, an
    // unavailable table, and a deployment with no query provider all arrive as
    // no field at all, and none of them may show an empty shell.
    const { container: undefinedCase } = render(<ObservedEntities />);
    expect(undefinedCase).toBeEmptyDOMElement();

    const { container: emptyCase } = render(<ObservedEntities observed={[]} />);
    expect(emptyCase).toBeEmptyDOMElement();
  });

  it("states what the entity is queryable as and how many rows it holds", () => {
    render(<ObservedEntities observed={[entity()]} />);

    expect(screen.getByText("Observed Now")).toBeInTheDocument();
    expect(screen.getByText("iceberg.retail.orders")).toBeInTheDocument();
    expect(screen.getByText("primary")).toBeInTheDocument();
    expect(screen.getByText(/Queryable now, currently ~1,200 rows\./)).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("says so when the connection does not estimate row counts", () => {
    render(<ObservedEntities observed={[entity({ estimated_rows: undefined })]} />);

    expect(
      screen.getByText("Queryable now. This connection does not estimate row counts."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("flags a claim that disagrees with the table, without blocking it", () => {
    render(
      <ObservedEntities
        observed={[
          entity({
            conflict: {
              claimed_rows: 1140,
              observed_rows: 1200,
              message: "claim states 1140; the table currently estimates 1200",
            },
          }),
        ]}
      />,
    );

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Claim disagrees with the table");
    expect(alert).toHaveTextContent(
      "Claim states 1,140; the table currently estimates 1,200.",
    );
    expect(alert).toHaveTextContent(/Advisory only/);
  });

  it("falls back to the URN when the provider named no query table", () => {
    render(<ObservedEntities observed={[entity({ query_table: undefined })]} />);
    expect(screen.getByText(urn)).toBeInTheDocument();
  });

  it("renders one card per observed entity", () => {
    render(
      <ObservedEntities
        observed={[
          entity(),
          entity({ urn: `${urn}-2`, query_table: "iceberg.retail.daily_sales" }),
        ]}
      />,
    );

    expect(screen.getByText("iceberg.retail.orders")).toBeInTheDocument();
    expect(screen.getByText("iceberg.retail.daily_sales")).toBeInTheDocument();
  });
});
