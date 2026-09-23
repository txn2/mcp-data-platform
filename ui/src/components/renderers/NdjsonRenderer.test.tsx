import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { NdjsonRenderer } from "./NdjsonRenderer";

afterEach(cleanup);

const EXPORT = [
  '{"id":1,"Region":"West","tags":["a","b"],"total":10.5}',
  '{"id":2,"region":"East","tags":[],"total":null}',
  '{"id":3,"Region":"North","tags":["c"],"total":3}',
].join("\n");

const EVENT_LOG = [
  '{"event":"login","user":"a"}',
  '{"event":"click","target":{"id":"x","path":{"deep":[1]}}}',
  '{"kind":"error","code":500,"trace":"t"}',
].join("\n");

describe("NdjsonRenderer", () => {
  it("opens a table-shaped file on the Table view, columns in first-seen order and first spelling", () => {
    render(<NdjsonRenderer content={EXPORT} />);
    expect(screen.getByRole("button", { name: "Table" })).toHaveAttribute("aria-pressed", "true");
    const heads = screen.getAllByRole("columnheader").map((h) => h.textContent);
    expect(heads).toEqual(["id", "Region", "tags", "total"]);
    // "region" on line 2 is the same column as "Region" on line 1.
    expect(screen.getByText("East")).toBeInTheDocument();
    expect(screen.getByText('["a","b"]')).toBeInTheDocument();
  });

  it("opens an event log of different shapes on the Records view", () => {
    render(<NdjsonRenderer content={EVENT_LOG} />);
    expect(screen.getByRole("button", { name: "Records" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.queryByRole("columnheader")).toBeNull();
  });

  it("toggles to the record list and back", () => {
    render(<NdjsonRenderer content={EXPORT} />);
    fireEvent.click(screen.getByRole("button", { name: "Records" }));
    expect(screen.queryByRole("columnheader")).toBeNull();
    expect(screen.getByText(/"Region":"West"/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Table" }));
    expect(screen.getAllByRole("columnheader")).toHaveLength(4);
  });

  it("searches and sorts the table, and opens a nested value in the row dialog", () => {
    render(<NdjsonRenderer content={EXPORT} />);
    fireEvent.change(screen.getByLabelText("Search all columns"), { target: { value: "north" } });
    expect(screen.getAllByRole("button", { name: /^Open row/ })).toHaveLength(1);
    fireEvent.change(screen.getByLabelText("Search all columns"), { target: { value: "" } });

    fireEvent.click(screen.getByRole("columnheader", { name: /total/ }));
    const rows = screen.getAllByRole("button", { name: /^Open row/ });
    expect(within(rows[0] as HTMLElement).getByText("North")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Open row 2" }));
    const fields = screen.getByTestId("row-detail-fields");
    expect(within(fields).getByText(/"a",\s+"b"/)).toBeInTheDocument();
  });

  it("keeps the raw view for a file with no records", () => {
    render(<NdjsonRenderer content={"\n\n"} />);
    expect(screen.queryByRole("button", { name: "Table" })).toBeNull();
  });

  it("opens on Records when a line does not parse", () => {
    render(<NdjsonRenderer content={'{"a":1}\nnot json\n'} />);
    expect(screen.getByRole("button", { name: "Records" })).toHaveAttribute("aria-pressed", "true");
  });
});
