import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";
import type { APIRouteConnection, APIRouteRule } from "@/api/admin/types";
import { ApiScopeGroups } from "./ApiScopeGroups";
import { ApiRulesSection } from "./ApiRulesSection";
import { resolveRoute } from "./apiRoutes";

// The persona editor's API-endpoint scope (#1479). What these hold is that the
// list shows the operations a rule can be written against, that selecting one
// writes that operation's own method and declared path, and that the three
// decisions an operator has to tell apart are told apart on the row.

afterEach(cleanup);

const ORDER_PATH = "/v1/orders/{id}";

const crm: APIRouteConnection = {
  name: "acme-crm",
  description: "ACME CRM",
  base_url: "https://crm.example.com",
  catalog_id: "crm-2025",
  operations: [
    { operation_id: "listOrders", method: "GET", path: "/v1/orders", summary: "List orders" },
    { operation_id: "getOrder", method: "GET", path: ORDER_PATH, summary: "Get an order" },
    {
      operation_id: "deleteOrder",
      method: "DELETE",
      path: ORDER_PATH,
      summary: "Delete an order",
    },
  ],
};

function renderGroups(rules: APIRouteRule[], handlers = {
  setOperation: vi.fn(),
  setConnection: vi.fn(),
}) {
  render(
    <ApiScopeGroups
      connections={[crm]}
      resolve={(connection, op) =>
        resolveRoute(rules, connection, op.method.toUpperCase(), op.path)
      }
      statusFilter="all"
      search=""
      selected={null}
      setSelected={vi.fn()}
      setHovered={vi.fn()}
      handlers={handlers}
      isLoading={false}
      governedBy={() => false}
    />,
  );
  return handlers;
}

describe("ApiScopeGroups", () => {
  it("lists every operation the connection's catalog declares", () => {
    renderGroups([]);
    expect(screen.getByLabelText("GET /v1/orders")).toBeInTheDocument();
    expect(screen.getByLabelText(`GET ${ORDER_PATH}`)).toBeInTheDocument();
    expect(screen.getByLabelText(`DELETE ${ORDER_PATH}`)).toBeInTheDocument();
  });

  it("says an unruled connection is reachable without claiming a rule allows it", () => {
    // An operator reading a page of allow ticks would conclude rules are in
    // force where none are, and would not know the connection grant is the
    // only gate.
    renderGroups([]);
    expect(screen.getByLabelText("GET /v1/orders")).toHaveAttribute(
      "title",
      "Reachable: no rule names this connection",
    );
  });

  it("marks the operation a deny rule refuses, and only that one", () => {
    renderGroups([
      { connection: "acme-crm" },
      { connection: "acme-crm", methods: ["DELETE"], paths: [ORDER_PATH], action: "deny" },
    ]);
    expect(screen.getByLabelText(`DELETE ${ORDER_PATH}`)).toHaveAttribute(
      "title",
      "Denied",
    );
    expect(screen.getByLabelText(`GET ${ORDER_PATH}`)).toHaveAttribute(
      "title",
      "Allowed by a rule",
    );
    expect(screen.getByLabelText("GET /v1/orders")).toHaveAttribute(
      "title",
      "Allowed by a rule",
    );
  });

  it("hands the selected operation to the handler that compiles its rule", () => {
    const handlers = renderGroups([]);
    fireEvent.click(screen.getByLabelText(`deny DELETE ${ORDER_PATH}`));
    expect(handlers.setOperation).toHaveBeenCalledWith(
      "acme-crm",
      expect.objectContaining({ method: "DELETE", path: ORDER_PATH }),
      "deny",
    );
  });

  it("counts what the persona reaches against the catalog's total", () => {
    renderGroups([
      { connection: "acme-crm" },
      { connection: "acme-crm", methods: ["DELETE"], action: "deny" },
    ]);
    expect(screen.getByText("2/3 reachable")).toBeInTheDocument();
  });

  it("says why a connection with no catalog has nothing to select", () => {
    render(
      <ApiScopeGroups
        connections={[{ name: "raw-api", operations: [] }]}
        resolve={() => ({ decision: "open" })}
        statusFilter="all"
        search=""
        selected={null}
        setSelected={vi.fn()}
        setHovered={vi.fn()}
        handlers={{ setOperation: vi.fn(), setConnection: vi.fn() }}
        isLoading={false}
        governedBy={() => false}
      />,
    );
    expect(screen.getByText(/No catalog is loaded for this connection/)).toBeInTheDocument();
  });

  it("tells a deployment with no API connections that rules apply to api kind only", () => {
    render(
      <ApiScopeGroups
        connections={[]}
        resolve={() => ({ decision: "open" })}
        statusFilter="all"
        search=""
        selected={null}
        setSelected={vi.fn()}
        setHovered={vi.fn()}
        handlers={{ setOperation: vi.fn(), setConnection: vi.fn() }}
        isLoading={false}
        governedBy={() => false}
      />,
    );
    expect(screen.getByText(/No API connections are configured/)).toBeInTheDocument();
  });

  it("narrows the list by the search text", () => {
    render(
      <ApiScopeGroups
        connections={[crm]}
        resolve={() => ({ decision: "open" })}
        statusFilter="all"
        search="delete"
        selected={null}
        setSelected={vi.fn()}
        setHovered={vi.fn()}
        handlers={{ setOperation: vi.fn(), setConnection: vi.fn() }}
        isLoading={false}
        governedBy={() => false}
      />,
    );
    expect(screen.getByLabelText(`DELETE ${ORDER_PATH}`)).toBeInTheDocument();
    expect(screen.queryByLabelText("GET /v1/orders")).not.toBeInTheDocument();
  });

  it("narrows the list to what is denied", () => {
    render(
      <ApiScopeGroups
        connections={[crm]}
        resolve={(connection, op) =>
          resolveRoute(
            [
              { connection: "acme-crm" },
              { connection: "acme-crm", methods: ["DELETE"], action: "deny" },
            ],
            connection,
            op.method.toUpperCase(),
            op.path,
          )
        }
        statusFilter="denied"
        search=""
        selected={null}
        setSelected={vi.fn()}
        setHovered={vi.fn()}
        handlers={{ setOperation: vi.fn(), setConnection: vi.fn() }}
        isLoading={false}
        governedBy={() => false}
      />,
    );
    expect(screen.getByLabelText(`DELETE ${ORDER_PATH}`)).toBeInTheDocument();
    expect(screen.queryByLabelText("GET /v1/orders")).not.toBeInTheDocument();
  });
});

describe("ApiRulesSection", () => {
  function renderRules(rules: APIRouteRule[], onAdd = vi.fn(), onRemove = vi.fn()) {
    render(
      <ApiRulesSection
        rules={rules}
        connectionNames={["acme-crm"]}
        onAdd={onAdd}
        onRemove={onRemove}
        highlightIndex={null}
        onHighlight={vi.fn()}
      />,
    );
    return { onAdd, onRemove };
  }

  it("shows a hand-written glob as the glob it was typed as", () => {
    // A rule the operator wrote must round-trip to them unchanged rather than
    // being rendered as the selection it resembles.
    renderRules([{ connection: "acme-crm", paths: ["/v1/admin/*"], action: "deny" }]);
    expect(screen.getByText("/v1/admin/")).toBeInTheDocument();
    expect(screen.getByText("any method")).toBeInTheDocument();
  });

  it("writes a rule from the form, splitting the method and path lists", () => {
    const { onAdd } = renderRules([]);
    fireEvent.click(screen.getByRole("button", { name: /add rule/i }));
    fireEvent.change(screen.getByLabelText("Rule connection"), {
      target: { value: "acme-crm" },
    });
    fireEvent.change(screen.getByLabelText("Rule methods"), {
      target: { value: "get, head" },
    });
    fireEvent.change(screen.getByLabelText("Rule paths"), {
      target: { value: "/v1/orders/*" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));

    expect(onAdd).toHaveBeenCalledWith({
      connection: "acme-crm",
      methods: ["GET", "HEAD"],
      paths: ["/v1/orders/*"],
      action: "deny",
    });
  });

  it("leaves an omitted dimension undefined so it reads as any", () => {
    const { onAdd } = renderRules([]);
    fireEvent.click(screen.getByRole("button", { name: /add rule/i }));
    fireEvent.change(screen.getByLabelText("Rule connection"), {
      target: { value: "acme-crm" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));

    expect(onAdd).toHaveBeenCalledWith({
      connection: "acme-crm",
      methods: undefined,
      paths: undefined,
      action: "deny",
    });
  });

  it("removes a rule by its position", () => {
    const { onRemove } = renderRules([
      { connection: "acme-crm", action: "deny" },
      { connection: "acme-billing", action: "deny" },
    ]);
    fireEvent.click(screen.getByLabelText("remove rule acme-billing"));
    expect(onRemove).toHaveBeenCalledWith(1);
  });

  it("warns that an allow rule closes the rest of the connection it names", () => {
    renderRules([{ connection: "acme-crm", methods: ["GET"], action: "allow" }]);
    expect(
      screen.getByText(/An allow rule closes the rest of the connection it names/),
    ).toBeInTheDocument();
  });

  it("says nothing about closing when every rule is a deny", () => {
    const { container } = render(
      <ApiRulesSection
        rules={[{ connection: "acme-crm", action: "deny" }]}
        connectionNames={[]}
        onAdd={vi.fn()}
        onRemove={vi.fn()}
        highlightIndex={null}
        onHighlight={vi.fn()}
      />,
    );
    expect(
      within(container).queryByText(/An allow rule closes/),
    ).not.toBeInTheDocument();
  });
});
