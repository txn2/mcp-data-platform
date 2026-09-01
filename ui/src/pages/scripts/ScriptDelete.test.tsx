import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { ScriptDelete } from "./ScriptDelete";

vi.mock("@/api/portal/hooks/scriptDelete", () => ({
  useDeleteScript: vi.fn(),
}));

import { useDeleteScript } from "@/api/portal/hooks/scriptDelete";

const mockDelete = vi.mocked(useDeleteScript);

const contract: ScriptContract = {
  id: "script-001",
  name: "daily-sales-report",
  display_name: "Daily Sales Report",
  owner_email: "sarah.chen@example.com",
  status: "active",
  enabled: true,
  params: [],
  version: 4,
  schedule: {
    cron_spec: "0 7 * * 1-5",
    timezone: "America/Los_Angeles",
    enabled: true,
  },
  state: { reads_state: true, saves_state: true, revision: 12 },
};

let mutateAsync = vi.fn();
let onDeleted = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  mutateAsync = vi.fn().mockResolvedValue({ status: "deleted", name: contract.name, message: "" });
  onDeleted = vi.fn();
  mockDelete.mockReturnValue({ mutateAsync, isPending: false } as never);
});

afterEach(cleanup);

function renderControl(overrides: Partial<ScriptContract> = {}) {
  render(
    <ScriptDelete
      scriptId="script-001"
      contract={{ ...contract, ...overrides }}
      onDeleted={onDeleted}
    />,
  );
}

// open walks to the confirmation, which is the only place the delete can be
// asked for: the control itself never removes anything.
function open() {
  fireEvent.click(screen.getByRole("button", { name: "Delete script" }));
}

// confirm presses the destructive button inside the dialog, which is a second
// element bearing the same name as the control that opened it.
function confirm() {
  const buttons = screen.getAllByRole("button", { name: "Delete script" });
  fireEvent.click(buttons[buttons.length - 1]!);
}

describe("ScriptDelete", () => {
  it("asks nothing of the platform until the confirmation is accepted", () => {
    renderControl();

    open();

    expect(screen.getByText("Delete Daily Sales Report?")).toBeInTheDocument();
    expect(mutateAsync).not.toHaveBeenCalled();
  });

  it("names what goes with the script before the delete runs", () => {
    renderControl();

    open();

    expect(screen.getByText(/Every saved version of its code/)).toBeInTheDocument();
    expect(screen.getByText(/including v4, the version a run executes/)).toBeInTheDocument();
    expect(screen.getByText(/Its whole run history/)).toBeInTheDocument();
    expect(screen.getByText(/The state it carries from one run to the next/)).toBeInTheDocument();
  });

  it("states the schedule that stops, in words rather than as a cron expression", () => {
    renderControl();

    open();

    const line = screen.getByText(/Its schedule,/);
    expect(line.textContent).toContain("America/Los_Angeles");
    expect(line.textContent).not.toContain("0 7 * * 1-5");
  });

  it("names a paused schedule as paused, so it is not read as one already gone", () => {
    renderControl({
      schedule: { cron_spec: "0 7 * * 1-5", timezone: "UTC", enabled: false },
    });

    open();

    expect(screen.getByText(/Its schedule,.*\(paused\)/)).toBeInTheDocument();
  });

  it("says nothing about a schedule or state the script does not have", () => {
    renderControl({ schedule: undefined, state: undefined });

    open();

    expect(screen.queryByText(/Its schedule,/)).not.toBeInTheDocument();
    expect(screen.queryByText(/The state it carries/)).not.toBeInTheDocument();
    expect(screen.getByText(/Its whole run history/)).toBeInTheDocument();
  });

  it("says what the delete does NOT take, which is what a person is likeliest to have wrong", () => {
    renderControl();

    open();

    expect(
      screen.getByText(/assets and resources it wrote stay where they are/),
    ).toBeInTheDocument();
    expect(screen.getByText(/go on recording that this script wrote them/)).toBeInTheDocument();
  });

  it("removes the script and leaves the page once the confirmation is accepted", async () => {
    renderControl();

    open();
    confirm();

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(onDeleted).toHaveBeenCalledTimes(1));
  });

  it("keeps the reader on the page and shows why when the delete fails", async () => {
    mutateAsync = vi.fn().mockRejectedValue(new Error("failed to delete script"));
    mockDelete.mockReturnValue({ mutateAsync, isPending: false } as never);
    renderControl();

    open();
    confirm();

    await waitFor(() =>
      expect(screen.getByText("failed to delete script")).toBeInTheDocument(),
    );
    expect(onDeleted).not.toHaveBeenCalled();
  });

  it("clears a failure when the dialog is dismissed, so a retry does not open onto it", async () => {
    mutateAsync = vi.fn().mockRejectedValue(new Error("failed to delete script"));
    mockDelete.mockReturnValue({ mutateAsync, isPending: false } as never);
    renderControl();

    open();
    confirm();
    await waitFor(() =>
      expect(screen.getByText("failed to delete script")).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    open();

    expect(screen.queryByText("failed to delete script")).not.toBeInTheDocument();
  });

  it("falls back to the script's name when it carries no display name", () => {
    renderControl({ display_name: undefined });

    open();

    expect(screen.getByText("Delete daily-sales-report?")).toBeInTheDocument();
  });
});
