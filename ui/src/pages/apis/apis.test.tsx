import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { FailedSpec } from "@/api/apis/hooks";
import type { APIConnection, APIOperationDetail, APIOperationSummary } from "@/api/apis/types";
import { ApisPage } from "./ApisPage";
import { OperationIndex, groupOperations } from "./OperationIndex";
import { OperationDetail } from "./OperationDetail";
import { CallSnippet, curlSnippet, invokeBody, sampleBody } from "./CallSnippet";
import { useBrowseDetail, useBrowseIndex, useBrowseSources } from "./useApiBrowse";

// The operation browser (#1478). What these hold is that the page shows what a
// reader reaches and nothing else, that an operation's parameters, body and
// responses are readable without opening the spec, and that the snippet it
// hands over is the call the gateway would actually make.

vi.mock("./useApiBrowse", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./useApiBrowse")>()),
  useBrowseSources: vi.fn(),
  useBrowseIndex: vi.fn(),
  useBrowseDetail: vi.fn(),
}));

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/portal/apis");
});

function op(overrides: Partial<APIOperationSummary> = {}): APIOperationSummary {
  return {
    operation_id: "listCustomers",
    method: "GET",
    path: "/v1/customers",
    summary: "List customers",
    tags: ["Customers"],
    spec: "core",
    ...overrides,
  };
}

function connection(overrides: Partial<APIConnection> = {}): APIConnection {
  return {
    name: "acme-billing",
    description: "ACME billing",
    base_url: "https://api.stripe.com",
    auth_mode: "bearer",
    catalog_id: "stripe-api-2025-01",
    operation_count: 2,
    specs: [{ name: "core", title: "Stripe Core", operation_count: 2 }],
    ...overrides,
  };
}

function detail(overrides: Partial<APIOperationDetail> = {}): APIOperationDetail {
  return {
    spec: "core",
    operation_id: "createCustomer",
    method: "POST",
    path: "/v1/customers",
    summary: "Create a customer",
    description: "Creates a new customer object.",
    parameters: [
      {
        name: "customer",
        in: "path",
        required: true,
        description: "The identifier of the customer.",
        schema: { type: "string" },
      },
      { name: "limit", in: "query", schema: { type: "integer" } },
    ],
    request_body: {
      required: true,
      content_types: ["application/json"],
      schema: {
        type: "object",
        required: ["email"],
        properties: {
          email: { type: "string", format: "email", description: "The customer's email." },
          name: { type: "string" },
        },
      },
    },
    responses: [
      { status: "200", description: "The created customer." },
      { status: "400", description: "The request was malformed." },
    ],
    ...overrides,
  };
}

/** indexRow is the clickable row for one path. The pane shows the same path, so
 * the row is found by being the button that carries it. */
function indexRow(path: string): HTMLElement {
  const row = screen.getAllByRole("button").find((b) => b.textContent?.includes(path));
  if (!row) throw new Error(`no index row for ${path}`);
  return row;
}

function mockPage(options: {
  sources?: { value: string; label: string; hint?: string }[];
  operations?: APIOperationSummary[];
  conn?: APIConnection;
  failedSpecs?: FailedSpec[];
  detail?: APIOperationDetail;
}) {
  vi.mocked(useBrowseSources).mockReturnValue({
    options: options.sources ?? [{ value: "acme-billing", label: "acme-billing" }],
    isLoading: false,
  });
  vi.mocked(useBrowseIndex).mockReturnValue({
    operations: options.operations ?? [],
    connection: options.conn,
    failedSpecs: options.failedSpecs ?? [],
    isLoading: false,
  });
  vi.mocked(useBrowseDetail).mockReturnValue({
    detail: options.detail,
    isLoading: false,
  });
}

describe("the operation index", () => {
  it("groups by spec and tag so the index reads like the documents it came from", () => {
    const groups = groupOperations([
      op({ operation_id: "listCharges", path: "/v1/charges", tags: ["Charges"] }),
      op(),
      op({ operation_id: "listInvoices", path: "/v1/invoices", spec: "billing", tags: ["Invoices"] }),
    ]);

    expect(groups.map((g) => `${g.spec}/${g.tag}`)).toEqual([
      "billing/Invoices",
      "core/Charges",
      "core/Customers",
    ]);
  });

  it("lists an operation under every tag it carries, because it belongs to each", () => {
    const groups = groupOperations([op({ tags: ["Customers", "Search"] })]);

    expect(groups.map((g) => g.tag)).toEqual(["Customers", "Search"]);
  });

  it("gives an untagged operation a group rather than dropping it", () => {
    const groups = groupOperations([op({ tags: undefined })]);

    expect(groups).toHaveLength(1);
    expect(groups[0]!.tag).toBe("Untagged");
    expect(groups[0]!.operations).toHaveLength(1);
  });

  it("filters over the operation id, the path and the summary", () => {
    render(
      <OperationIndex
        operations={[op(), op({ operation_id: "createCharge", method: "POST", path: "/v1/charges", summary: "Create a charge", tags: ["Charges"] })]}
        selected={null}
        onSelect={vi.fn()}
        emptyMessage="nothing here"
      />,
    );

    fireEvent.change(screen.getByRole("textbox", { name: "Filter operations" }), {
      target: { value: "charge" },
    });

    expect(screen.getByText("/v1/charges")).toBeInTheDocument();
    expect(screen.queryByText("/v1/customers")).not.toBeInTheDocument();
  });

  it("says the filter matched nothing rather than that there is nothing", () => {
    render(
      <OperationIndex
        operations={[op()]}
        selected={null}
        onSelect={vi.fn()}
        emptyMessage="This connection exposes no operations you can reach."
      />,
    );

    fireEvent.change(screen.getByRole("textbox", { name: "Filter operations" }), {
      target: { value: "nothing matches this" },
    });

    expect(screen.getByText(/No operation matches/)).toBeInTheDocument();
    expect(
      screen.queryByText("This connection exposes no operations you can reach."),
    ).not.toBeInTheDocument();
  });

  it("says what is missing when the source itself is empty", () => {
    render(
      <OperationIndex
        operations={[]}
        selected={null}
        onSelect={vi.fn()}
        emptyMessage="This catalog has no operations."
      />,
    );

    expect(screen.getByText("This catalog has no operations.")).toBeInTheDocument();
  });

  // A link may carry the operation and not the spec, which the detail route
  // resolves without one. The row must still light, or the reader gets the
  // operation open with nothing in the index showing where they are.
  it("lights the row for a selection that names no spec", () => {
    const { container } = render(
      <OperationIndex
        operations={[op()]}
        selected={{ operationID: "listCustomers", spec: "" }}
        onSelect={vi.fn()}
        emptyMessage="nothing here"
      />,
    );

    expect(container.querySelectorAll("button.bg-primary\\/10")).toHaveLength(1);
  });

  it("reports the selection as an operation and the spec that defines it", () => {
    const onSelect = vi.fn();
    render(
      <OperationIndex
        operations={[op()]}
        selected={null}
        onSelect={onSelect}
        emptyMessage="nothing here"
      />,
    );

    fireEvent.click(screen.getByText("/v1/customers"));

    expect(onSelect).toHaveBeenCalledWith({ operationID: "listCustomers", spec: "core" });
  });
});

describe("the operation pane", () => {
  it("shows the parameters, the body and the responses, and marks what is required", () => {
    render(<OperationDetail detail={detail()} />);

    expect(screen.getByText("Path parameters")).toBeInTheDocument();
    expect(screen.getByText("Query parameters")).toBeInTheDocument();
    expect(screen.getByText("Request body")).toBeInTheDocument();
    expect(screen.getByText("Responses")).toBeInTheDocument();
    expect(screen.getByText("The customer's email.")).toBeInTheDocument();
    expect(screen.getByText("400")).toBeInTheDocument();
    // The required path parameter and the required body; the query parameter
    // is the one marked optional.
    expect(screen.getAllByText("required").length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText("optional")).toBeInTheDocument();
  });

  it("asks for a selection rather than rendering an empty pane", () => {
    render(<OperationDetail detail={undefined} />);

    expect(screen.getByText(/Select an operation/)).toBeInTheDocument();
  });

  it("says why an operation could not be read", () => {
    render(<OperationDetail detail={undefined} error="operation not found" />);

    expect(screen.getByText("operation not found")).toBeInTheDocument();
  });

  it("offers no call snippet where there is no connection to call", () => {
    render(<OperationDetail detail={detail()} />);

    expect(screen.queryByText("Call it over HTTP")).not.toBeInTheDocument();
  });

  it("offers the call snippet on a connection", () => {
    render(
      <OperationDetail
        detail={detail()}
        connection={connection()}
        origin="https://platform.example.com"
      />,
    );

    expect(screen.getByText("Call it over HTTP")).toBeInTheDocument();
    expect(screen.getByText(/bearer/)).toBeInTheDocument();
  });
});

describe("the call snippet", () => {
  it("posts to the gateway route with the operation's own method and path", () => {
    const snippet = curlSnippet("https://platform.example.com", "acme-billing", detail());

    expect(snippet).toContain(
      "https://platform.example.com/api/v1/gateway/acme-billing/invoke",
    );
    expect(snippet).toContain('"method": "POST"');
    expect(snippet).toContain('"path": "/v1/customers"');
  });

  it("carries the required parameters and leaves the optional ones out", () => {
    const body = invokeBody(detail());

    expect(body.query_params).toBeUndefined();
    expect(body.method).toBe("POST");
  });

  it("includes a required query parameter", () => {
    const body = invokeBody(
      detail({
        parameters: [{ name: "limit", in: "query", required: true, schema: { type: "integer" } }],
      }),
    );

    expect(body.query_params).toEqual({ limit: 0 });
  });

  it("writes the smallest body the schema declares", () => {
    expect(
      sampleBody({
        type: "object",
        required: ["email"],
        properties: {
          email: { type: "string" },
          name: { type: "string" },
        },
      }),
    ).toEqual({ email: "<string>" });
  });

  it("answers an enumeration with a value the endpoint accepts", () => {
    expect(
      sampleBody({
        type: "object",
        required: ["currency"],
        properties: { currency: { type: "string", enum: ["usd", "eur"] } },
      }),
    ).toEqual({ currency: "usd" });
  });

  it("leaves a path parameter as the placeholder the caller must replace", () => {
    const snippet = curlSnippet(
      "https://platform.example.com",
      "acme-billing",
      detail({ path: "/v1/customers/{customer}" }),
    );

    expect(snippet).toContain("/v1/customers/{customer}");
  });

  // The gateway route decodes the body into internal/httpserver/gatewayhttp's
  // invokeRequest: method, path, query_params, headers, body, timeout_seconds.
  // A key this builder emits that is not in that set is silently dropped by the
  // decoder, so the snippet would run and do the wrong thing.
  it("emits only the keys the gateway route decodes", () => {
    const accepted = ["method", "path", "query_params", "headers", "body", "timeout_seconds"];
    const body = invokeBody(
      detail({
        parameters: [
          { name: "customer", in: "path", required: true, schema: { type: "string" } },
          { name: "limit", in: "query", required: true, schema: { type: "integer" } },
          { name: "X-Trace", in: "header", required: true, schema: { type: "string" } },
        ],
      }),
    );

    expect(Object.keys(body).sort()).toEqual(
      ["method", "path", "query_params", "headers", "body"].sort(),
    );
    for (const key of Object.keys(body)) {
      expect(accepted).toContain(key);
    }
    // A path parameter is not a body key: it stays in the path template, which
    // is what the caller replaces.
    expect(body.query_params).toEqual({ limit: 0 });
    expect(body.headers).toEqual({ "X-Trace": "<string>" });
  });

  it("closes and reopens the quoting around an apostrophe in the spec", () => {
    const snippet = curlSnippet(
      "https://platform.example.com",
      "acme-billing",
      detail({
        request_body: {
          required: true,
          schema: {
            type: "object",
            required: ["greeting"],
            properties: { greeting: { type: "string", enum: ["it's here"] } },
          },
        },
      }),
    );

    // The payload's apostrophe must not end the shell's quoted string, or the
    // command a reader copies does not run.
    expect(snippet).toContain(`'\\''`);
    expect(snippet).not.toMatch(/-d '[^']*it's/);
  });

  it("names the upstream call the gateway will make", () => {
    render(
      <CallSnippet
        connection="acme-billing"
        baseURL="https://api.stripe.com"
        detail={detail()}
        origin="https://platform.example.com"
      />,
    );

    expect(screen.getByText("POST https://api.stripe.com/v1/customers")).toBeInTheDocument();
  });
});

describe("the browser page", () => {
  it("shows the operations of the source it opened on", () => {
    mockPage({ operations: [op()], conn: connection() });

    render(<ApisPage scope="portal" />);

    expect(screen.getByText("/v1/customers")).toBeInTheDocument();
    expect(screen.getByText("https://api.stripe.com")).toBeInTheDocument();
  });

  it("tells a caller who reaches no connection what is missing", () => {
    mockPage({ sources: [] });

    render(<ApisPage scope="portal" />);

    expect(screen.getByText(/No API connection is available to you/)).toBeInTheDocument();
  });

  it("tells an operator with no catalog what is missing, in their terms", () => {
    mockPage({ sources: [] });

    render(<ApisPage scope="admin" />);

    expect(screen.getByText(/No API catalog has been created/)).toBeInTheDocument();
  });

  it("names a spec whose content does not parse, and says that is what is wrong", () => {
    mockPage({ operations: [op()], failedSpecs: [{ name: "billing", unparseable: true }] });

    render(<ApisPage scope="admin" />);

    expect(screen.getByText(/billing could not be read as OpenAPI/)).toBeInTheDocument();
  });

  // A store outage says nothing about the spec's content, and reporting it as
  // malformed would send the operator to edit a spec that is fine.
  it("does not call a spec malformed when the read simply did not land", () => {
    mockPage({ operations: [op()], failedSpecs: [{ name: "billing", unparseable: false }] });

    render(<ApisPage scope="admin" />);

    expect(screen.getByText(/billing could not be loaded/)).toBeInTheDocument();
    expect(screen.queryByText(/could not be read as OpenAPI/)).not.toBeInTheDocument();
  });

  it("puts the selection in the address bar so an operation can be linked to", () => {
    mockPage({ operations: [op()], conn: connection(), detail: detail() });

    render(<ApisPage scope="portal" />);
    fireEvent.click(indexRow("/v1/customers"));

    const params = new URLSearchParams(window.location.search);
    expect(params.get("connection")).toBe("acme-billing");
    expect(params.get("op")).toBe("listCustomers");
    expect(params.get("spec")).toBe("core");
  });

  it("opens on the operation a link names", () => {
    window.history.replaceState(
      null,
      "",
      "/portal/apis?connection=acme-billing&op=createCustomer&spec=core",
    );
    mockPage({ operations: [op()], conn: connection(), detail: detail() });

    render(<ApisPage scope="portal" />);

    expect(screen.getByText("Create a customer")).toBeInTheDocument();
  });

  it("names the source by what it holds in each scope", () => {
    mockPage({ operations: [op()] });
    const { unmount } = render(<ApisPage scope="portal" />);
    expect(screen.getByRole("combobox", { name: "Choose an API connection" })).toBeInTheDocument();
    unmount();

    mockPage({ operations: [op()] });
    render(<ApisPage scope="admin" />);
    expect(screen.getByRole("combobox", { name: "Choose an API catalog" })).toBeInTheDocument();
  });
});
