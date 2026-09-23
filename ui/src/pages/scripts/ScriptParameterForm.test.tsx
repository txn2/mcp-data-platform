import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptParam } from "@/api/portal/hooks/scripts";
import {
  boundParams,
  declaresConnection,
  listValues,
  missingRequired,
  orderedParams,
  ScriptParameterForm,
  valuesFrom,
} from "./ScriptParameterForm";

// The rule this component exists to enforce (#1361): where the platform knows
// the set a value comes from, it offers the set. A free-text box is for values
// it genuinely cannot enumerate.

afterEach(cleanup);

const connections = [
  { name: "warehouse", kind: "trino", description: "Production warehouse" },
  { name: "lake", kind: "s3" },
];

function form(params: ScriptParam[], extra: Record<string, unknown> = {}) {
  const onChange = vi.fn();
  render(
    <ScriptParameterForm
      form="run"
      params={params}
      values={{}}
      disabled={false}
      onChange={onChange}
      {...extra}
    />,
  );
  return onChange;
}

describe("ScriptParameterForm", () => {
  it("offers a connection as a choice with its description, never as a box", () => {
    const onChange = form([{ name: "source", type: "connection", required: true }], {
      connections,
    });

    const control = screen.getByLabelText("source");
    fireEvent.click(control);
    expect(screen.getByRole("option", { name: "warehouse — Production warehouse" })).toBeInTheDocument();
    // A connection with no description is still offered, by name alone.
    fireEvent.click(screen.getByRole("option", { name: "lake" }));
    expect(onChange).toHaveBeenCalledWith("source", "lake");
  });

  it("stays a picker before the set has been read rather than changing control under the reader", () => {
    form([{ name: "source", type: "connection", required: true }]);

    expect(screen.getByLabelText("source")).toHaveAttribute("role", "combobox");
  });

  it("says so when a script may reach no connection at all", () => {
    form([{ name: "source", type: "connection", required: true }], { connections: [] });

    expect(screen.getByText(/No connection is available/)).toBeInTheDocument();
    expect(screen.getByLabelText("source")).toBeDisabled();
  });

  it("offers an enum's declared values and a bool's two", () => {
    form([
      { name: "grain", type: "enum", required: true, values: ["daily", "weekly"] },
      { name: "detailed", type: "bool", required: true },
    ]);

    fireEvent.click(screen.getByLabelText("grain"));
    expect(screen.getByRole("option", { name: "daily" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "weekly" })).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("detailed"));
    expect(screen.getByRole("option", { name: "true" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "false" })).toBeInTheDocument();
  });

  it("takes a box for a value the platform cannot enumerate", () => {
    const onChange = form([{ name: "region", type: "string", required: true }]);

    fireEvent.change(screen.getByLabelText("region"), { target: { value: "west" } });
    expect(onChange).toHaveBeenCalledWith("region", "west");
  });

  it("names the fire token only where a schedule is being written", () => {
    const { rerender } = render(
      <ScriptParameterForm
        form="run"
        params={[{ name: "day", type: "date", required: true }]}
        values={{}}
        disabled={false}
        onChange={vi.fn()}
      />,
    );
    expect(screen.queryByText(/fire_date/)).not.toBeInTheDocument();

    rerender(
      <ScriptParameterForm
        form="schedule"
        params={[{ name: "day", type: "date", required: true }]}
        values={{}}
        disabled={false}
        onChange={vi.fn()}
        scheduled
      />,
    );
    expect(screen.getByText(/fire_date/)).toBeInTheDocument();
  });

  it("renders nothing for a script that declares no parameters", () => {
    const { container } = render(
      <ScriptParameterForm form="run" params={[]} values={{}} disabled={false} onChange={vi.fn()} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});

describe("parameter form helpers", () => {
  const params: ScriptParam[] = [
    { name: "day", type: "date", required: true },
    { name: "region", type: "string", required: false },
    { name: "source", type: "connection", required: true },
  ];

  it("declaresConnection is what decides whether the set is asked for at all", () => {
    expect(declaresConnection(params)).toBe(true);
    expect(declaresConnection(params.slice(0, 1))).toBe(false);
  });

  it("boundParams drops an empty box rather than sending a value the contract refuses", () => {
    expect(boundParams(params, { day: "2026-08-17", region: "", source: "warehouse" })).toEqual({
      day: "2026-08-17",
      source: "warehouse",
    });
  });

  it("boundParams is driven by the contract, so a stale value is dropped", () => {
    expect(boundParams(params, { day: "2026-08-17", retired: "x", source: "wh" })).toEqual({
      day: "2026-08-17",
      source: "wh",
    });
  });

  it("missingRequired names what is unbound, and counts a default as supplied", () => {
    expect(missingRequired(params, { day: "2026-08-17" })).toEqual(["source"]);
    expect(
      missingRequired([{ name: "day", type: "date", required: true, default: "2026-01-01" }], {}),
    ).toEqual([]);
  });

  it("valuesFrom renders stored bindings as the strings an input holds", () => {
    expect(valuesFrom({ day: "2026-08-17", count: 3, missing: null })).toEqual({
      day: "2026-08-17",
      count: "3",
      missing: "",
    });
    expect(valuesFrom(undefined)).toEqual({});
  });
});

// A script's page carries up to three of these forms at once — run now, dry
// run, and the schedule's bindings — and a DOM id may appear once in a
// document. Two controls sharing one would make the second form's label point
// at the first form's control.
describe("ScriptParameterForm: three forms on one page", () => {
  it("scopes each control's id to the form it belongs to", () => {
    const params: ScriptParam[] = [{ name: "day", type: "date", required: true }];
    const { container } = render(
      <>
        <ScriptParameterForm
          form="run"
          params={params}
          values={{}}
          disabled={false}
          onChange={vi.fn()}
        />
        <ScriptParameterForm
          form="draft"
          params={params}
          values={{}}
          disabled={false}
          onChange={vi.fn()}
        />
      </>,
    );

    const ids = [...container.querySelectorAll("[id^='script-param-']")].map((el) => el.id);
    expect(ids).toEqual(["script-param-run-day", "script-param-draft-day"]);
    expect(new Set(ids).size).toBe(ids.length);
  });
});

// #1844: list and date_range parameters, and the form metadata.
describe("ScriptParameterForm: lists, ranges and form metadata", () => {
  const listParam: ScriptParam = { name: "ids", type: "list", items: "string", required: true };
  const regions: ScriptParam = {
    name: "regions", type: "list", items: "enum", values: ["west", "east"], required: false,
    label: "Regions", group: "Filters",
  };
  const period: ScriptParam = { name: "period", type: "date_range", required: true, order: -1 };

  it("sends a list as an array and a date range as {from, to}", () => {
    expect(
      boundParams([listParam, regions, period], {
        ids: "a, b,\nc", regions: "west", period: "2026-09-01..2026-09-30",
      }),
    ).toEqual({ ids: ["a", "b", "c"], regions: ["west"], period: { from: "2026-09-01", to: "2026-09-30" } });
    expect(boundParams([period], { period: ".." })).toEqual({});
  });

  it("seeds a form from stored list and range bindings", () => {
    expect(valuesFrom({ ids: ["a", "b"], period: { from: "2026-09-01", to: "2026-09-02" } })).toEqual({
      ids: "a, b", period: "2026-09-01..2026-09-02",
    });
    expect(listValues(" a ,, b ")).toEqual(["a", "b"]);
  });

  it("orders by order, then as declared", () => {
    expect(orderedParams([listParam, regions, period]).map((p) => p.name)).toEqual(["period", "ids", "regions"]);
  });

  it("renders a label, a group heading, checkboxes for an enum list and two dates for a range", () => {
    const onChange = vi.fn();
    render(
      <ScriptParameterForm params={[listParam, regions, period]} values={{ regions: "west" }} disabled={false} onChange={onChange} form="run" />,
    );
    expect(screen.getByText("Filters")).toBeInTheDocument();
    expect(screen.getByText("Regions (optional)")).toBeInTheDocument();
    expect(screen.getByText("Several string values, separated by commas.")).toBeInTheDocument();
    const east = screen.getByRole("checkbox", { name: "east" });
    fireEvent.click(east);
    expect(onChange).toHaveBeenCalledWith("regions", "west, east");
    fireEvent.change(screen.getByLabelText("to"), { target: { value: "2026-09-30" } });
    expect(onChange).toHaveBeenCalledWith("period", "..2026-09-30");
    fireEvent.change(screen.getByLabelText("from"), { target: { value: "2026-09-01" } });
    expect(onChange).toHaveBeenCalledWith("period", "2026-09-01..");
  });
});

describe("ScriptParameterForm: caller-bound parameters", () => {
  const tenant: ScriptParam = { name: "tenant", type: "string", required: true, bind: "caller.tenant" };
  const region: ScriptParam = { name: "region", type: "string", required: false };

  it("offers no field, sends no value and never calls it missing", () => {
    const { container } = render(
      <ScriptParameterForm params={[tenant]} values={{}} disabled={false} onChange={vi.fn()} form="run" />,
    );
    expect(container).toBeEmptyDOMElement();
    expect(orderedParams([tenant, region]).map((p) => p.name)).toEqual(["region"]);
    expect(boundParams([tenant, region], { tenant: "globex", region: "west" })).toEqual({ region: "west" });
    expect(missingRequired([tenant], {})).toEqual([]);
  });
});
