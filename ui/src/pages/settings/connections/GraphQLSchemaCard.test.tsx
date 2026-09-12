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

import { useGraphQLSchema, type GraphQLSchemaInfo } from "@/api/admin/hooks";
import { GraphQLSchemaCard } from "./GraphQLSchemaCard";

const mockSchema = vi.mocked(useGraphQLSchema);

type SchemaQuery = ReturnType<typeof useGraphQLSchema>;

function renderCard(info: GraphQLSchemaInfo) {
  mockSchema.mockReturnValue({
    data: info,
    isLoading: false,
    error: null,
  } as unknown as SchemaQuery);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <GraphQLSchemaCard connectionName={info.connection} isReadOnly={false} />
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
