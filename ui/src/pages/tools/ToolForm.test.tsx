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

// The connection picker is a Radix listbox, not a native <select>: jsdom has no
// PointerEvent, so the trigger's pointerdown handler never runs and the list is
// opened with a keypress instead. The chosen value reaches the form through the
// hidden input the picker keeps in sync.
function openListbox() {
  fireEvent.keyDown(screen.getByRole("combobox", { name: "connection" }), {
    key: "Enter",
  });
}

function hiddenConnectionValue(container: HTMLElement): string {
  return container.querySelector<HTMLInputElement>('input[type="hidden"][name="connection"]')!
    .value;
}

function enumToolSchema(): ToolSchema {
  return {
    name: "trino_query",
    kind: "trino",
    connection: "primary",
    parameters: {
      type: "object",
      properties: {
        format: {
          type: "string",
          description: "Output format for results",
          enum: ["table", "json", "csv"],
          default: "table",
        },
      },
      required: [],
    },
  } as unknown as ToolSchema;
}

function hiddenValue(container: HTMLElement, name: string): string {
  return container.querySelector<HTMLInputElement>(
    `input[type="hidden"][name="${name}"]`,
  )!.value;
}

// A one-of field opens on the value the schema (or a replayed audit event)
// names, not empty: the listbox is a Radix control whose value only reaches the
// form through a hidden input, so the starting selection has to be carried into
// that input rather than left to a `defaultValue` the listbox does not have.
describe("ToolForm one-of fields", () => {
  it("opens an enum field on the schema's default", () => {
    const { container } = render(
      <ToolForm
        schema={enumToolSchema()}
        selectedConnection="primary"
        isSubmitting={false}
        onSubmit={vi.fn()}
      />,
    );
    expect(hiddenValue(container, "format")).toBe("table");
  });

  it("opens an enum field on a replayed event's value instead of the default", () => {
    const onSubmit = vi.fn();
    const { container } = render(
      <ToolForm
        schema={enumToolSchema()}
        selectedConnection="primary"
        initialValues={{ format: "csv" }}
        isSubmitting={false}
        onSubmit={onSubmit}
      />,
    );
    expect(hiddenValue(container, "format")).toBe("csv");

    fireEvent.submit(screen.getByRole("button", { name: /execute/i }).closest("form")!);
    expect(onSubmit).toHaveBeenCalledWith({ format: "csv" });
  });

  // Once a value is picked the trigger's placeholder is unreachable, so an
  // optional parameter needs the unset choice as an item of the list — without
  // it a mis-click can never be undone and the value is sent on every run.
  it("lets an optional enum be returned to unset", () => {
    const onSubmit = vi.fn();
    const { container } = render(
      <ToolForm
        schema={enumToolSchema()}
        selectedConnection="primary"
        isSubmitting={false}
        onSubmit={onSubmit}
      />,
    );
    expect(hiddenValue(container, "format")).toBe("table");

    fireEvent.keyDown(screen.getByRole("combobox", { name: "format" }), { key: "Enter" });
    fireEvent.click(screen.getByRole("option", { name: "-- select --" }));
    expect(hiddenValue(container, "format")).toBe("");

    fireEvent.submit(screen.getByRole("button", { name: /execute/i }).closest("form")!);
    expect(onSubmit).toHaveBeenCalledWith({});
  });
});

describe("ToolForm connection picker", () => {
  it("locks the connection picker when selectedConnection is bound", () => {
    const { container } = render(
      <ToolForm
        schema={apiToolSchema()}
        selectedConnection="salesforce"
        isSubmitting={false}
        onSubmit={vi.fn()}
      />,
    );
    expect(screen.getByRole("combobox", { name: "connection" })).toBeDisabled();
    expect(hiddenConnectionValue(container)).toBe("salesforce");
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
    const trigger = screen.getByRole("combobox", { name: "connection" });
    expect(trigger).not.toBeDisabled();

    openListbox();
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual([
      "salesforce",
      "github",
    ]);
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
    expect(screen.getByRole("combobox", { name: "connection" })).toBeDisabled();
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
    openListbox();
    fireEvent.click(screen.getByRole("option", { name: "salesforce" }));

    fireEvent.submit(screen.getByRole("button", { name: /execute/i }).closest("form")!);
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0]![0]).toMatchObject({ connection: "salesforce" });
  });

  // A listbox carries its value in a hidden input, which the browser excludes
  // from constraint validation — so nothing stops the submit and nothing says
  // why. Without the form's own report, Execute is a dead click.
  it("refuses a submit missing a required choice, and names the field", () => {
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
    fireEvent.submit(screen.getByRole("button", { name: /execute/i }).closest("form")!);

    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText(/fill in connection before executing/i)).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "connection" })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });

  // TryItTab keeps `connection` in a history entry's captured parameters so a
  // replay re-runs against the same target; the picker has to open on it.
  it("opens on the connection a replayed call ran against", () => {
    const { container } = render(
      <ToolForm
        schema={apiToolSchema()}
        selectedConnection=""
        availableConnections={[conn("api", "salesforce"), conn("api", "github")]}
        initialValues={{ connection: "github" }}
        isSubmitting={false}
        onSubmit={vi.fn()}
      />,
    );
    expect(hiddenConnectionValue(container)).toBe("github");
  });
});
