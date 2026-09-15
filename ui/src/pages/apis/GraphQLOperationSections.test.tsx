import { describe, it, expect } from "vitest";
import { render, screen, within } from "@testing-library/react";

import type { APIOperationDetail } from "@/api/apis/types";
import { OperationDetail } from "./OperationDetail";

// A catalog may hold a GraphQL schema, and an administrator browses its
// operations in this pane beside every OpenAPI one (#1745). A GraphQL
// operation says what it takes and gives back in arguments, the input types
// they reference and the shape it returns, so the pane has to render those
// where it renders parameters, a body and responses -- otherwise the row is
// listed and opening it shows nothing.

const graphqlOperation: APIOperationDetail = {
  spec: "schema",
  operation_id: "query:catalog.product",
  method: "QUERY",
  path: "/catalog/product",
  summary: "Find one product by id.",
  return_type: "Product",
  graphql_arguments: [
    { name: "id", type: "ID!", required: true, description: "The product's id." },
    { name: "locale", type: "String", default: '"en-US"' },
  ],
  graphql_input_types: [
    {
      name: "ProductFilter",
      description: "Narrows a product search.",
      fields: [{ name: "category", type: "String" }],
    },
  ],
  graphql_return_shape: [
    { name: "id", type: "ID!" },
    { name: "name", type: "String" },
    {
      name: "supplier",
      type: "Supplier",
      fields: [{ name: "name", type: "String" }],
    },
  ],
  graphql_skeleton:
    "query Product($id: ID!) {\n  catalog {\n    product(id: $id) {\n      id\n      name\n    }\n  }\n}",
  graphql_variables: '{\n  "id": ""\n}',
};

// section returns the block a heading labels, so an assertion about one
// part of the pane is not answered by another.
function section(heading: string): HTMLElement {
  return screen.getByText(heading).parentElement as HTMLElement;
}

const openAPIOperation: APIOperationDetail = {
  spec: "orders",
  operation_id: "listOrders",
  method: "GET",
  path: "/v1/orders",
  summary: "List orders.",
  parameters: [{ name: "limit", in: "query", required: false }],
};

describe("OperationDetail for a GraphQL operation", () => {
  it("names the operation by its kind and the route a rule names it under", () => {
    render(<OperationDetail detail={graphqlOperation} />);

    expect(screen.getByText("QUERY")).toBeInTheDocument();
    expect(screen.getByText("/catalog/product")).toBeInTheDocument();
    expect(screen.getByText("query:catalog.product")).toBeInTheDocument();
    expect(screen.getByText("Find one product by id.")).toBeInTheDocument();
  });

  it("renders the arguments, saying which a document must supply", () => {
    render(<OperationDetail detail={graphqlOperation} />);

    // Scoped to the section: a field of the return shape may share a name
    // with an argument, and both are rendered.
    const args = within(section("Arguments"));
    expect(args.getByText("id")).toBeInTheDocument();
    expect(args.getByText("ID!")).toBeInTheDocument();
    expect(args.getByText("The product's id.")).toBeInTheDocument();
    expect(args.getByText("required")).toBeInTheDocument();
    expect(args.getByText("locale")).toBeInTheDocument();
    expect(args.getByText("optional")).toBeInTheDocument();
    expect(args.getByText('default "en-US"')).toBeInTheDocument();
  });

  it("expands the input types an argument references", () => {
    render(<OperationDetail detail={graphqlOperation} />);

    expect(screen.getByText("Input types")).toBeInTheDocument();
    expect(screen.getByText("ProductFilter")).toBeInTheDocument();
    expect(screen.getByText("Narrows a product search.")).toBeInTheDocument();
    expect(screen.getByText("category")).toBeInTheDocument();
  });

  it("renders the return shape as a tree, nested fields included", () => {
    render(<OperationDetail detail={graphqlOperation} />);

    const returns = within(section("Returns"));
    expect(returns.getByText("Product")).toBeInTheDocument();
    expect(returns.getByText("supplier")).toBeInTheDocument();
    // The nested field is rendered under its parent, not flattened away.
    const supplier = returns.getByText("supplier").closest("li");
    expect(within(supplier as HTMLElement).getByText("name")).toBeInTheDocument();
  });

  it("carries the document that already calls it, and its variables", () => {
    render(<OperationDetail detail={graphqlOperation} />);

    expect(screen.getByText("Document")).toBeInTheDocument();
    expect(screen.getByText(/product\(id: \$id\)/)).toBeInTheDocument();
    expect(screen.getByText("Variables")).toBeInTheDocument();
    expect(screen.getByText(/"id": ""/)).toBeInTheDocument();
  });

  it("renders none of those sections for an OpenAPI operation", () => {
    render(<OperationDetail detail={openAPIOperation} />);

    expect(screen.getByText("GET")).toBeInTheDocument();
    expect(screen.queryByText("Arguments")).not.toBeInTheDocument();
    expect(screen.queryByText("Input types")).not.toBeInTheDocument();
    expect(screen.queryByText("Returns")).not.toBeInTheDocument();
    expect(screen.queryByText("Document")).not.toBeInTheDocument();
  });
});
