import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { EffectiveConnection, ToolSchema } from "@/api/admin/types";
import { ToolForm } from "./ToolForm";

function apiToolSchema(): ToolSchema {
  return {
    name: "api_list_endpoints",
    title: "List API Endpoints",
    description: "List operations of a registered api connection",
    kind: "api",
    connection: "",
    parameters: {
      type: "object",
      properties: {
        connection: {
          type: "string",
          description: "Name of the registered API connection (kind=api).",
        },
        query: { type: "string", description: "Search query" },
      },
      required: ["connection"],
    },
  } as unknown as ToolSchema;
}

function conn(kind: string, name: string): EffectiveConnection {
  return {
    kind,
    name,
    connection: name,
    description: "",
    source: "database",
    tools: [],
  };
}

describe("ToolForm connection picker", () => {
  it("locks the connection select when selectedConnection is bound", () => {
    render(
      <ToolForm
        schema={apiToolSchema()}
        selectedConnection="salesforce"
        isSubmitting={false}
        onSubmit={vi.fn()}
      />,
    );
    const select = screen.getByRole("combobox") as HTMLSelectElement;
    expect(select).toBeDisabled();
    expect(select.value).toBe("salesforce");
  });

  it("renders an enabled picker listing availableConnections when none is bound", () => {
    render(
      <ToolForm
        schema={apiToolSchema()}
        selectedConnection=""
        availableConnections={[
          conn("api", "salesforce"),
          conn("api", "github"),
        ]}
        isSubmitting={false}
        onSubmit={vi.fn()}
      />,
    );
    const select = screen.getByRole("combobox") as HTMLSelectElement;
    expect(select).not.toBeDisabled();
    expect(select.name).toBe("connection");
    const optionValues = Array.from(select.options).map((o) => o.value);
    expect(optionValues).toEqual(["", "salesforce", "github"]);
  });

  it("shows a helper message when no connections of the tool's kind exist", () => {
    render(
      <ToolForm
        schema={apiToolSchema()}
        selectedConnection=""
        availableConnections={[]}
        isSubmitting={false}
        onSubmit={vi.fn()}
      />,
    );
    expect(
      screen.getByText(/no api connections registered/i),
    ).toBeInTheDocument();
    const select = screen.getByRole("combobox") as HTMLSelectElement;
    expect(select).toBeDisabled();
  });

  it("submits the operator's connection choice in params.connection", () => {
    const onSubmit = vi.fn();
    render(
      <ToolForm
        schema={apiToolSchema()}
        selectedConnection=""
        availableConnections={[conn("api", "salesforce")]}
        isSubmitting={false}
        onSubmit={onSubmit}
      />,
    );
    fireEvent.change(screen.getByRole("combobox") as HTMLSelectElement, {
      target: { value: "salesforce" },
    });
    fireEvent.submit(screen.getByRole("button", { name: /execute/i }).closest("form")!);
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0]![0]).toMatchObject({ connection: "salesforce" });
  });

  it("does not submit while connection is required-but-empty", () => {
    const onSubmit = vi.fn();
    render(
      <ToolForm
        schema={apiToolSchema()}
        selectedConnection=""
        availableConnections={[conn("api", "salesforce")]}
        isSubmitting={false}
        onSubmit={onSubmit}
      />,
    );
    // Click Execute without picking a connection: required-validation
    // on the browser-side <select required> blocks the submit.
    const submit = screen.getByRole("button", { name: /execute/i });
    fireEvent.click(submit);
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
