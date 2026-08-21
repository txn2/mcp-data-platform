import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, act } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { ScriptRunPanel } from "./ScriptRunPanel";

// Running a script from its own page (#1363). What matters here is what the
// panel submits, what it refuses to submit, and that it never offers a control
// for a script the platform would not run.
vi.mock("@/api/portal/hooks/scripts", () => ({
  useRunScript: vi.fn(),
  useScriptConnections: vi.fn(),
}));

import { useRunScript, useScriptConnections } from "@/api/portal/hooks/scripts";

const mockRun = vi.mocked(useRunScript);
const mockConnections = vi.mocked(useScriptConnections);
const run = vi.fn();

const contract: ScriptContract = {
  id: "script-001",
  name: "daily-sales-report",
  display_name: "Daily Sales Report",
  owner_email: "sarah.chen@example.com",
  status: "active",
  enabled: true,
  params: [
    { name: "report_date", type: "date", description: "The business date.", required: true },
    { name: "source", type: "connection", required: true },
  ],
  version: 4,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockRun.mockReturnValue({ mutate: run, isPending: false } as never);
  mockConnections.mockReturnValue({
    data: {
      data: [{ name: "warehouse", kind: "trino", description: "Production warehouse" }],
      source: "persona",
      note: "These are the connections your persona reaches that a script may query.",
    },
    isLoading: false,
    error: null,
  } as never);
});

afterEach(cleanup);

describe("ScriptRunPanel", () => {
  it("submits the bound values and reports where the run went", () => {
    render(<ScriptRunPanel scriptId="script-001" contract={contract} />);

    fireEvent.change(screen.getByLabelText("report_date"), {
      target: { value: "2026-08-17" },
    });
    // The connection is chosen, not typed: the platform holds the set.
    fireEvent.click(screen.getByLabelText("source"));
    fireEvent.click(screen.getByRole("option", { name: /warehouse/ }));
    fireEvent.click(screen.getByRole("button", { name: "Run" }));

    expect(run).toHaveBeenCalledTimes(1);
    const [params, handlers] = run.mock.calls[0]!;
    expect(params).toEqual({ report_date: "2026-08-17", source: "warehouse" });

    act(() => handlers.onSuccess({ run_id: "run_1", message: "Queued." }));
    expect(screen.getByText("Queued.")).toBeInTheDocument();
  });

  it("asks for the persona set only when a parameter binds a connection", () => {
    render(<ScriptRunPanel scriptId="script-001" contract={contract} />);

    expect(mockConnections).toHaveBeenCalledWith("script-001", true);
  });

  it("does not ask for connections for a script that names none", () => {
    render(
      <ScriptRunPanel
        scriptId="script-001"
        contract={{ ...contract, params: contract.params.slice(0, 1) }}
      />,
    );

    expect(mockConnections).toHaveBeenCalledWith("script-001", false);
  });

  it("names the required values still unbound instead of submitting a refusal", () => {
    render(<ScriptRunPanel scriptId="script-001" contract={contract} />);

    expect(screen.getByRole("button", { name: "Run" })).toBeDisabled();
    expect(screen.getByText(/report_date, source are required/)).toBeInTheDocument();
    expect(run).not.toHaveBeenCalled();
  });

  it("reports a refused run in place, in the server's words", () => {
    render(<ScriptRunPanel scriptId="script-001" contract={{ ...contract, params: [] }} />);
    fireEvent.click(screen.getByRole("button", { name: "Run" }));

    act(() => run.mock.calls[0]![1].onError(new Error("the script is disabled")));
    expect(screen.getByText("the script is disabled")).toBeInTheDocument();
  });

  it("offers no control at all for a script nothing would execute", () => {
    render(
      <ScriptRunPanel
        scriptId="script-001"
        contract={{ ...contract, refusal: "the script is disabled, so a run would be refused" }}
      />,
    );

    expect(screen.queryByRole("button", { name: "Run" })).not.toBeInTheDocument();
    expect(screen.getByText(/would be refused, for the reason stated above/)).toBeInTheDocument();
  });

  it("names the version it would run, so nobody runs a draft by accident", () => {
    render(<ScriptRunPanel scriptId="script-001" contract={contract} />);

    expect(screen.getByText(/This runs version 4/)).toBeInTheDocument();
  });
});
