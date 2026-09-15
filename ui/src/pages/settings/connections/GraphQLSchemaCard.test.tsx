import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

vi.mock("@/api/admin/hooks", () => ({
  useGraphQLSchema: vi.fn(),
  useRefreshGraphQLSchema: vi.fn(() => ({
    mutate: vi.fn(),
    isPending: false,
    error: null,
  })),
}));

import {
  useGraphQLSchema,
  useRefreshGraphQLSchema,
  type GraphQLSchemaInfo,
} from "@/api/admin/hooks";
import { GraphQLSchemaCard } from "./GraphQLSchemaCard";

const mockSchema = vi.mocked(useGraphQLSchema);

type SchemaQuery = ReturnType<typeof useGraphQLSchema>;

function renderCard(info: GraphQLSchemaInfo, catalogID?: string) {
  mockSchema.mockReturnValue({
    data: info,
    isLoading: false,
    error: null,
  } as unknown as SchemaQuery);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <GraphQLSchemaCard
        connectionName={info.connection}
        isReadOnly={false}
        catalogID={catalogID}
      />
    </QueryClientProvider>,
  );
}

// A read the endpoint refuses keeps the schema the connection holds and records
// the refusal beside it (#1676). The card read any error as "no schema", so an
// operator whose uploaded schema was serving discovery was told it was gone and
// uploaded it again (#1689).
describe("GraphQLSchemaCard schema state", () => {
  it("shows a held schema and the failed re-read together", () => {
    renderCard({
      connection: "acme-orders-graphql",
      schema_hash: "ff68d87b41c2a9e30b5d7c18aa4f6921",
      source: "upload",
      fetched_at: "2025-01-21T08:30:00Z",
      operation_count: 8,
      error: "graphql: the endpoint answered HTTP 302 to the introspection query: ",
    });

    expect(screen.getByText("8")).toBeInTheDocument();
    expect(screen.getByText(/operations/)).toBeInTheDocument();
    expect(screen.getByText("uploaded")).toBeInTheDocument();
    expect(screen.getByText("ff68d87b41c2")).toBeInTheDocument();
    expect(
      screen.getByText(/last attempt to re-read this schema/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/HTTP 302/)).toBeInTheDocument();
    expect(screen.queryByText(/holds no schema/i)).not.toBeInTheDocument();
  });

  it("reports no schema when the platform holds none", () => {
    renderCard({
      connection: "acme-partners-graphql",
      operation_count: 0,
      error: "graphql: GraphQL introspection is not allowed",
    });

    expect(
      screen.getByText(/holds no schema for this connection: graphql/i),
    ).toBeInTheDocument();
    expect(screen.queryByText(/operations/)).not.toBeInTheDocument();
    expect(screen.queryByText("uploaded")).not.toBeInTheDocument();
  });

  // A connection registered but not yet read has neither a schema nor a named
  // cause; the alert must not trail an empty colon.
  it("names no cause when there is none to name", () => {
    renderCard({ connection: "acme-new-graphql", operation_count: 0 });

    expect(
      screen.getByText("The platform holds no schema for this connection yet."),
    ).toBeInTheDocument();
  });

  // The count is the operation index's, and a schema is held whenever the
  // platform reports its hash, so a schema exposing nothing callable is still
  // a schema and not an absence.
  it("treats a hash with no operations as a held schema", () => {
    renderCard({
      connection: "acme-empty-graphql",
      schema_hash: "0d1e2f3a4b5c6d7e",
      source: "introspection",
      fetched_at: "2025-01-21T08:30:00Z",
      operation_count: 0,
    });

    expect(screen.getByText(/operations/)).toBeInTheDocument();
    expect(screen.getByText("introspected")).toBeInTheDocument();
    expect(screen.queryByText(/holds no schema/i)).not.toBeInTheDocument();
  });
});

// The refresh route answers a re-read the endpoint refused as the state it
// left, a 200 with `error` filled, because a CDN in front of a deployment
// replaced the body of the 502 it used to answer and the button printed
// "Request failed with status 502" (#1704). The button's report comes from
// that state.
describe("GraphQLSchemaCard re-read outcome", () => {
  const held: GraphQLSchemaInfo = {
    connection: "acme-orders-graphql",
    schema_hash: "ff68d87b41c2a9e30b5d7c18aa4f6921",
    source: "upload",
    fetched_at: "2025-01-21T08:30:00Z",
    operation_count: 8,
    error: "graphql: the endpoint answered HTTP 302 to the introspection query: ",
  };

  function renderAfterRefresh(
    info: GraphQLSchemaInfo,
    outcome: { data?: GraphQLSchemaInfo; error?: Error },
  ) {
    vi.mocked(useRefreshGraphQLSchema).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      data: outcome.data,
      error: outcome.error ?? null,
    } as unknown as ReturnType<typeof useRefreshGraphQLSchema>);
    return renderCard(info);
  }

  it("points at the recorded refusal when the re-read was refused", () => {
    renderAfterRefresh(held, { data: held });

    expect(
      screen.getByText("The read failed, for the reason already shown above."),
    ).toBeInTheDocument();
    expect(screen.getAllByText(/HTTP 302/)).toHaveLength(1);
  });

  it("reports nothing under the button when the read installed a schema", () => {
    const fresh = { ...held, source: "introspection", error: undefined };
    renderAfterRefresh(fresh, { data: fresh });

    expect(screen.queryByText(/The read failed/)).not.toBeInTheDocument();
    expect(screen.queryByText(/last attempt to re-read/i)).not.toBeInTheDocument();
  });

  it("prints a refused upload's cause, which is recorded nowhere", () => {
    const clean = { ...held, error: undefined };
    renderAfterRefresh(clean, {
      error: new Error("graphql: parsing the schema: unexpected end of input"),
    });

    expect(screen.getByText(/unexpected end of input/)).toBeInTheDocument();
  });
});

// A connection that names a catalog takes its schema from there (#1745). The
// card has to say so and send the operator to the one place the schema is
// edited: an upload here would be replaced on the next read and would differ
// from what every other connection on that catalog serves, which is why the
// platform refuses one.
describe("GraphQLSchemaCard on a catalog-backed connection", () => {
  const catalogued: GraphQLSchemaInfo = {
    connection: "acme-erp-graphql",
    schema_hash: "a1b2c3d4e5f67890abcdef0123456789",
    source: "catalog",
    fetched_at: "2025-01-21T08:30:00Z",
    operation_count: 42,
  };

  it("names the catalog as the source and re-reads from it", () => {
    renderCard(catalogued, "acme-erp-2026-01");

    expect(screen.getByText("from catalog")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /re-read from catalog/i }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /re-read from endpoint/i }),
    ).not.toBeInTheDocument();
  });

  it("offers no upload, and says where the edit belongs", () => {
    renderCard(catalogued, "acme-erp-2026-01");

    expect(
      screen.queryByRole("button", { name: /upload a schema/i }),
    ).not.toBeInTheDocument();
    expect(screen.getByText(/takes its schema from a catalog/i)).toBeInTheDocument();
    expect(screen.getByText(/API Catalogs/)).toBeInTheDocument();
  });

  it("leaves a connection with no catalog reading its own endpoint", () => {
    renderCard({ ...catalogued, source: "introspection" });

    expect(screen.getByText("introspected")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /re-read from endpoint/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /upload a schema/i }),
    ).toBeInTheDocument();
    expect(screen.getByText(/Re-read it after the endpoint changes/i)).toBeInTheDocument();
  });
});
