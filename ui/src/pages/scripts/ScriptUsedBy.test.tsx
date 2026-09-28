import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { ScriptUsedBy, loadLine } from "./ScriptUsedBy";

afterEach(cleanup);

const library: ScriptContract = {
  id: "script-006",
  name: "date-windows",
  display_name: "Date Windows",
  owner_email: "sarah.chen@example.com",
  status: "active",
  enabled: true,
  params: [],
  version: 3,
  library: true,
  loads: [],
  used_by: [
    {
      script_id: "script-001",
      name: "daily-sales-report",
      display_name: "Daily Sales Report",
      owner_email: "sarah.chen@example.com",
      version: 3,
    },
    {
      script_id: "script-009",
      name: "weekly-rollup",
      display_name: "",
      owner_email: "marcus.webb@example.com",
      version: 1,
    },
  ],
};

function renderCard(contract: ScriptContract, onNavigate = vi.fn()) {
  render(<ScriptUsedBy contract={contract} basePath="/admin/automations" onNavigate={onNavigate} />);
  return onNavigate;
}

describe("ScriptUsedBy", () => {
  it("states the line another script loads this version with", () => {
    renderCard(library);

    expect(screen.getByRole("heading", { name: "Used by" })).toBeInTheDocument();
    expect(screen.getByTestId("library-load-line")).toHaveTextContent(
      'load("lib:date-windows@3", ...)',
    );
    expect(loadLine("fmt", 12)).toBe('load("lib:fmt@12", ...)');
  });

  it("lists each script that loads it with its author and the version it loads", () => {
    renderCard(library);

    const sales = within(screen.getByTestId("used-by-script-001"));
    expect(sales.getByText("Daily Sales Report")).toBeInTheDocument();
    expect(sales.getByText("daily-sales-report")).toBeInTheDocument();
    expect(sales.getByText(/sarah\.chen/)).toBeInTheDocument();
    expect(sales.getByText("version 3")).toBeInTheDocument();

    // A script with no display name is listed by its name.
    const rollup = within(screen.getByTestId("used-by-script-009"));
    expect(rollup.getAllByText("weekly-rollup").length).toBeGreaterThan(0);
    expect(rollup.getByText("version 1")).toBeInTheDocument();
  });

  it("opens a script on a row click, under the section the reader is in", () => {
    const onNavigate = renderCard(library);

    fireEvent.click(screen.getByTestId("used-by-script-009"));
    expect(onNavigate).toHaveBeenCalledWith("/admin/automations/script-009");
  });

  it("says plainly when nothing loads it", () => {
    renderCard({ ...library, used_by: [] });

    expect(screen.getByText("No automation loads this library.")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    // The load line is there either way: it is how the first user loads it.
    expect(screen.getByTestId("library-load-line")).toBeInTheDocument();
  });
});
