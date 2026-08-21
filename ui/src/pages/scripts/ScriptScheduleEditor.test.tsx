import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, act } from "@testing-library/react";
import type { ScriptContract, ScriptSchedule } from "@/api/portal/hooks/scripts";
import { ScriptScheduleEditor } from "./ScriptScheduleEditor";

// The editor is the one place on these pages that changes anything, so every
// assertion here is about what an owner reads before they change it and what
// the page actually submits.
vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptConnections: vi.fn(),
  useScriptSchedule: vi.fn(),
  useSetScriptSchedule: vi.fn(),
  useSetScriptSchedulePaused: vi.fn(),
}));

import {
  useScriptConnections,
  useScriptSchedule,
  useSetScriptSchedule,
  useSetScriptSchedulePaused,
} from "@/api/portal/hooks/scripts";

const mockSchedule = vi.mocked(useScriptSchedule);
const mockSave = vi.mocked(useSetScriptSchedule);
const mockPause = vi.mocked(useSetScriptSchedulePaused);
const mockConnections = vi.mocked(useScriptConnections);

const save = vi.fn();
const pause = vi.fn();

function query<T>(data: T, extra: Record<string, unknown> = {}) {
  return { data, isLoading: false, error: null, ...extra } as never;
}

const contract: ScriptContract = {
  id: "script-001",
  name: "daily-sales-report",
  display_name: "Daily Sales Report",
  owner_email: "sarah.chen@example.com",
  status: "active",
  enabled: true,
  params: [
    { name: "report_date", type: "date", description: "The business date.", required: true },
  ],
  version: 2,
};

const schedule: ScriptSchedule = {
  id: "sched-001",
  script_id: "script-001",
  cron_spec: "0 7 * * 1-5",
  timezone: "America/Los_Angeles",
  params: { report_date: "${fire_date}" },
  enabled: true,
  next_run_at: "2026-08-16T14:00:00Z",
  missed_fires: 0,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockSchedule.mockReturnValue(query(schedule));
  mockSave.mockReturnValue({ mutate: save, isPending: false } as never);
  mockPause.mockReturnValue({ mutate: pause, isPending: false } as never);
  mockConnections.mockReturnValue(query(undefined));
});

afterEach(cleanup);

// The section is folded by default (#1407), so a test about the builder opens
// it first. What the folded header says is asserted separately, below.
function openSchedule() {
  fireEvent.click(screen.getByRole("button", { name: /^Schedule/ }));
}

function renderEditor(over: Partial<ScriptContract> = {}) {
  render(<ScriptScheduleEditor scriptId="script-001" contract={{ ...contract, ...over }} />);
  openSchedule();
}

describe("ScriptScheduleEditor: what is in force", () => {
  it("states the cadence in words, with the expression under it", () => {
    renderEditor();
    expect(
      screen.getByText(/Every weekday at 7:00 AM, America\/Los_Angeles/),
    ).toBeInTheDocument();
    expect(screen.getAllByText("0 7 * * 1-5").length).toBeGreaterThan(0);
    expect(screen.getByText(/next fire/)).toBeInTheDocument();
  });

  it("prefills the builder from the schedule in force, bindings included", () => {
    renderEditor();
    expect(screen.getByRole("button", { name: "Weekdays" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByLabelText("Time")).toHaveValue("07:00");
    expect(screen.getByLabelText("Timezone")).toHaveValue("America/Los_Angeles");
    expect(screen.getByLabelText("report_date")).toHaveValue("${fire_date}");
  });

  it("says a script with no schedule runs only when someone asks", () => {
    mockSchedule.mockReturnValue(query(null));
    renderEditor();
    expect(screen.getByText(/runs only when someone asks/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Set schedule" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Pause" })).not.toBeInTheDocument();
  });

  // A gap is the one thing a schedule cannot report by simply existing.
  it("reports missed fires and that they are never caught up on", () => {
    mockSchedule.mockReturnValue(query({ ...schedule, missed_fires: 3 }));
    renderEditor();
    expect(screen.getByText(/3 fires have been missed/)).toBeInTheDocument();
  });

  // An expression whose last fire has passed is a real state; a dash beside
  // "next fire" would read as a page that failed to load something.
  it("says an enabled schedule has nothing further due", () => {
    mockSchedule.mockReturnValue(query({ ...schedule, next_run_at: undefined }));
    renderEditor();
    expect(screen.getByText(/enabled, with no further fire due/)).toBeInTheDocument();
  });

  it("says a paused schedule is firing nothing", () => {
    mockSchedule.mockReturnValue(query({ ...schedule, enabled: false }));
    renderEditor();
    expect(screen.getByText(/paused, and firing nothing/)).toBeInTheDocument();
  });
});

describe("ScriptScheduleEditor: changing the cadence", () => {
  // The builder is the input and cron is the wire format: what the page
  // submits is derived from the choices, never typed by the person making them.
  it("submits the expression its choices mean, with the bindings every fire passes", () => {
    renderEditor();
    fireEvent.change(screen.getByLabelText("Time"), { target: { value: "06:30" } });
    fireEvent.change(screen.getByLabelText("Timezone"), { target: { value: "UTC" } });
    fireEvent.click(screen.getByRole("button", { name: "Update schedule" }));

    expect(save).toHaveBeenCalledWith(
      { cron: "30 6 * * 1-5", timezone: "UTC", params: { report_date: "${fire_date}" } },
      expect.anything(),
    );
  });

  it("submits a whole different cadence when the kind changes", () => {
    renderEditor();
    fireEvent.click(screen.getByRole("button", { name: "Daily" }));
    fireEvent.click(screen.getByRole("button", { name: "Update schedule" }));

    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({ cron: "0 7 * * *" }),
      expect.anything(),
    );
  });

  // An empty box is an unbound parameter, not an empty value: "" would be
  // refused for a date, and the contract's own refusal is the better answer.
  it("omits a parameter left empty rather than binding an empty value", () => {
    mockSchedule.mockReturnValue(query({ ...schedule, params: {} }));
    renderEditor();
    fireEvent.click(screen.getByRole("button", { name: "Update schedule" }));
    expect(save).toHaveBeenCalledWith(
      { cron: "0 7 * * 1-5", timezone: "America/Los_Angeles", params: {} },
      expect.anything(),
    );
  });

  // The editor sits at the same position in the tree for every script, so an
  // unsaved cadence must not survive onto a different one.
  it("does not carry a part-typed cadence onto another script", () => {
    const { rerender } = render(
      <ScriptScheduleEditor scriptId="script-001" contract={contract} />,
    );
    openSchedule();
    fireEvent.change(screen.getByLabelText("Time"), { target: { value: "23:45" } });

    mockSchedule.mockReturnValue(
      query({ ...schedule, script_id: "script-009", cron_spec: "0 9 * * *" }),
    );
    rerender(<ScriptScheduleEditor scriptId="script-009" contract={contract} />);
    openSchedule();
    expect(screen.getByLabelText("Time")).toHaveValue("09:00");
  });

  // The builder always means something, so the only way to submit nothing is to
  // empty the advanced field.
  it("cannot be submitted with an empty custom expression", () => {
    mockSchedule.mockReturnValue(query(null));
    renderEditor();
    expect(screen.getByRole("button", { name: "Set schedule" })).toBeEnabled();

    fireEvent.click(screen.getByRole("button", { name: "Custom" }));
    fireEvent.change(screen.getByLabelText("Cron expression"), { target: { value: "" } });
    expect(screen.getByRole("button", { name: "Set schedule" })).toBeDisabled();
  });

  it("reports a refused change in place, in the server's words", () => {
    renderEditor();
    fireEvent.click(screen.getByRole("button", { name: "Update schedule" }));
    const onError = save.mock.calls[0]![1].onError as (e: unknown) => void;
    act(() => onError(new Error('the cadence "every tuesday" is not a cron expression')));
    expect(screen.getByText(/not a cron expression/)).toBeInTheDocument();
  });

  it("pauses and resumes without touching the cadence", () => {
    renderEditor();
    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    expect(pause).toHaveBeenCalledWith(false, expect.anything());
    expect(save).not.toHaveBeenCalled();

    cleanup();
    mockSchedule.mockReturnValue(query({ ...schedule, enabled: false }));
    renderEditor();
    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    expect(pause).toHaveBeenCalledWith(true, expect.anything());
  });

  it("disables every control while a change is in flight", () => {
    mockSave.mockReturnValue({ mutate: save, isPending: true } as never);
    renderEditor();
    expect(screen.getByLabelText("Time")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Update schedule" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Pause" })).toBeDisabled();
  });
});

describe("ScriptScheduleEditor: a script nothing will execute", () => {
  const refused: Partial<ScriptContract> = {
    enabled: false,
    refusal: "the script is disabled, so a run would be refused",
  };

  // The notice is driven by the run gate's own refusal, carried on the
  // contract: a cadence on a disabled or retired script saves and stays inert,
  // and the page says so rather than implying a fire it cannot produce.
  it("says a saved schedule will execute nothing, and still lets it be saved", () => {
    renderEditor(refused);
    expect(screen.getByText(/nothing will execute it/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Update schedule" })).toBeEnabled();
  });

  it("says the same before there is a schedule at all", () => {
    mockSchedule.mockReturnValue(query(null));
    renderEditor(refused);
    expect(screen.getByText(/saves, and stays inert/)).toBeInTheDocument();
  });

  it("says nothing of the sort when the gate admits a run", () => {
    renderEditor();
    expect(screen.queryByText(/nothing will execute it/)).not.toBeInTheDocument();
  });
});

describe("ScriptScheduleEditor: a schedule that cannot be read", () => {
  // A section with no schedule to state does not fold: there is nothing to
  // state in a folded header, and "Schedule / Loading" would be two words for
  // one.
  it("says so instead of offering a form that would overwrite it", () => {
    mockSchedule.mockReturnValue(query(null, { error: new Error("boom") }));
    render(<ScriptScheduleEditor scriptId="script-001" contract={contract} />);
    expect(screen.getByText(/could not be read/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Cadence")).not.toBeInTheDocument();
  });

  it("says it is loading before it has an answer", () => {
    mockSchedule.mockReturnValue(query(undefined, { isLoading: true }));
    render(<ScriptScheduleEditor scriptId="script-001" contract={contract} />);
    expect(screen.getByText(/Loading the schedule/)).toBeInTheDocument();
  });
});

// The section is folded by default (#1407): the cadence is set once and read
// constantly, so the header states what the script does and the builder that
// changes it is behind a reveal.
describe("ScriptScheduleEditor: folded", () => {
  it("states what the script runs without being opened", () => {
    render(<ScriptScheduleEditor scriptId="script-001" contract={contract} />);
    expect(
      screen.getByText("Runs: Every weekday at 7:00 AM, America/Los_Angeles"),
    ).toBeInTheDocument();
    // The builder is behind the reveal rather than on the page.
    expect(screen.queryByLabelText("Time")).not.toBeInTheDocument();
  });

  it("says a script with no schedule is not scheduled", () => {
    mockSchedule.mockReturnValue(query(null));
    render(<ScriptScheduleEditor scriptId="script-001" contract={contract} />);
    expect(screen.getByText("Not scheduled")).toBeInTheDocument();
  });

  // A paused schedule is named as paused rather than as what it would do,
  // because what it does is nothing.
  it("names a paused schedule as paused", () => {
    mockSchedule.mockReturnValue(query({ ...schedule, enabled: false }));
    render(<ScriptScheduleEditor scriptId="script-001" contract={contract} />);
    expect(
      screen.getByText("Paused: Every weekday at 7:00 AM, America/Los_Angeles"),
    ).toBeInTheDocument();
  });

  // Pausing and resuming stay reachable without opening the builder: they are
  // what somebody comes to a folded schedule to do.
  it("keeps the pause control on the folded header", () => {
    render(<ScriptScheduleEditor scriptId="script-001" contract={contract} />);
    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    expect(pause).toHaveBeenCalledWith(false, expect.anything());
  });

  it("opens the builder when the heading is pressed", () => {
    render(<ScriptScheduleEditor scriptId="script-001" contract={contract} />);
    openSchedule();
    expect(screen.getByLabelText("Time")).toHaveValue("07:00");
  });
});
