import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";

import { FormatBadge } from "./badges";
import { SpecModal } from "./SpecModal";

// The modal's two mutations are mocked so the assertions are about what the
// form sends, which is the part a WSDL spec depends on: a format the operator
// never picks is a WSDL saved as an OpenAPI document and a connection that
// registers with no operations.
const upsertMutate = vi.fn();
const uploadMutate = vi.fn();

vi.mock("@/api/admin/hooks", () => ({
  useAPICatalogSpec: vi.fn(() => ({ data: undefined })),
  useUpsertAPICatalogSpec: vi.fn(() => ({ mutateAsync: upsertMutate, isPending: false })),
  useUploadAPICatalogSpec: vi.fn(() => ({ mutateAsync: uploadMutate, isPending: false })),
}));

beforeEach(() => {
  upsertMutate.mockReset().mockResolvedValue({});
  uploadMutate.mockReset().mockResolvedValue({});
});
afterEach(cleanup);

// firstCallArgs is the argument object the modal handed the mutation. Reading
// it through a helper keeps the assertions from indexing a possibly-empty
// calls array, which the type checker is right to object to even though every
// caller has already awaited the call.
function firstCallArgs(): unknown {
  const [call] = upsertMutate.mock.calls;
  return call?.[0];
}

function renderModal() {
  return render(
    <SpecModal catalogID="erp" onClose={() => {}} onSaved={() => {}} />,
  );
}

function fillName(name: string) {
  const input = screen.getByLabelText(/spec name/i);
  fireEvent.change(input, { target: { value: name } });
}

describe("SpecModal format", () => {
  it("defaults to OpenAPI, which is what every spec written before this was", async () => {
    renderModal();
    fillName("orders");
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(upsertMutate).toHaveBeenCalled());
    expect(firstCallArgs()).toMatchObject({
      source_kind: "inline",
      spec_format: "openapi",
    });
  });

  it("sends spec_format=wsdl once the operator picks it", async () => {
    renderModal();
    fillName("orders");
    fireEvent.click(screen.getByRole("button", { name: /wsdl/i }));
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(upsertMutate).toHaveBeenCalled());
    expect(firstCallArgs()).toMatchObject({ spec_format: "wsdl" });
  });

  it("marks the chosen format so the operator can see which one is active", () => {
    renderModal();
    const wsdl = screen.getByRole("button", { name: /wsdl/i });
    expect(wsdl).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(wsdl);
    expect(screen.getByRole("button", { name: /wsdl/i })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("explains what the chosen format means", () => {
    renderModal();
    fireEvent.click(screen.getByRole("button", { name: /wsdl/i }));
    expect(screen.getByText(/builds the SOAP envelope/i)).toBeInTheDocument();
  });

  // The format is orthogonal to the source, so it has to reach the URL route
  // too — a WSDL is routinely fetched from the service address with ?wsdl.
  it("carries the format through a URL-sourced spec", async () => {
    renderModal();
    fillName("orders");
    fireEvent.click(screen.getByRole("button", { name: /wsdl/i }));
    fireEvent.mouseDown(screen.getByRole("tab", { name: "URL" }));
    fireEvent.change(screen.getByLabelText(/spec url/i), {
      target: { value: "https://erp.example.org/Orders.svc?wsdl" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(upsertMutate).toHaveBeenCalled());
    expect(firstCallArgs()).toMatchObject({
      source_kind: "url",
      spec_format: "wsdl",
      source_url: "https://erp.example.org/Orders.svc?wsdl",
    });
  });
});

describe("FormatBadge", () => {
  it("renders nothing for OpenAPI, which is the default and the majority", () => {
    const { container } = render(<FormatBadge format="openapi" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when the field is absent on an older row", () => {
    const { container } = render(<FormatBadge format={undefined} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("marks a WSDL spec so the one row that differs is findable", () => {
    render(<FormatBadge format="wsdl" />);
    expect(screen.getByText("WSDL")).toBeInTheDocument();
  });

  // #1765: every format other than OpenAPI wore the WSDL badge and the WSDL
  // tooltip, so a catalog holding an SDL read as holding a SOAP service.
  it("badges a GraphQL spec as GraphQL, not as WSDL", () => {
    render(<FormatBadge format="graphql" />);
    expect(screen.getByText("GraphQL")).toBeInTheDocument();
    expect(screen.queryByText("WSDL")).not.toBeInTheDocument();
  });

  it("gives the GraphQL badge its own tooltip", () => {
    render(<FormatBadge format="graphql" />);
    expect(screen.getByTitle(/served to the graphql connections/i)).toBeInTheDocument();
  });
});

describe("SpecModal format-specific copy", () => {
  it("heads the paste box with the chosen format", () => {
    renderModal();
    expect(screen.getByLabelText(/OpenAPI YAML or JSON/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /wsdl/i }));
    expect(screen.getByLabelText(/^WSDL$/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /graphql/i }));
    expect(screen.getByLabelText(/GraphQL SDL/i)).toBeInTheDocument();
  });

  // The base path is a URL prefix the HTTP gateway joins onto an operation,
  // and an SDL is never served to it, so the field has nothing to act on.
  it("drops the gateway-only fields for an SDL and says why", () => {
    renderModal();
    expect(screen.getByLabelText(/base path/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /graphql/i }));
    expect(screen.queryByLabelText(/base path/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/^title/i)).not.toBeInTheDocument();
    expect(screen.getByText(/not read for this format/i)).toBeInTheDocument();
  });

  it("keeps a spec name and its content across a format change", async () => {
    renderModal();
    fillName("schema");
    fireEvent.change(screen.getByLabelText(/OpenAPI YAML or JSON/i), {
      target: { value: "type Query { a: String }" },
    });
    fireEvent.click(screen.getByRole("button", { name: /graphql/i }));
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(upsertMutate).toHaveBeenCalled());
    expect(firstCallArgs()).toMatchObject({
      spec_format: "graphql",
      content: "type Query { a: String }",
      specName: "schema",
    });
  });
});
