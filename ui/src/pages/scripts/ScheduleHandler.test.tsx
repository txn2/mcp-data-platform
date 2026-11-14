import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, act } from "@testing-library/react";
import type { ScriptContract, ScriptSchedule } from "@/api/portal/hooks/scripts";
import { ScheduleHandler, scheduleHeadline } from "./ScheduleHandler";

// One of a script's schedule handlers, or a new one (#1307, #2434). Every
// assertion here is about what an owner reads before they change a schedule
// and what the page actually submits. The Handlers section that lists the
// schedules, adds and deletes them has its own tests (ScriptHandlers.test.tsx).
vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptConnections: vi.fn(),
}));

vi.mock("@/api/portal/hooks/scriptHandlers", () => ({
  useSaveHandler: vi.fn(),
  useSetHandlerEnabled: vi.fn(),
}));

import { useScriptConnections } from "@/api/portal/hooks/scripts";
import { useSaveHandler, useSetHandlerEnabled } from "@/api/portal/hooks/scriptHandlers";

const mockSave = vi.mocked(useSaveHandler);
const mockPause = vi.mocked(useSetHandlerEnabled);
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
  params: [{ name: "report_date", type: "date", description: "The business date.", required: true }],
  version: 2,
};

const schedule: ScriptSchedule = {
  id: "sched-001",
  script_id: "",
  cron_spec: "0 7 * * 1-5",
  timezone: "America/Los_Angeles",
  params: { report_date: "${fire_date}" },
  enabled: true,
  next_run_at: "2026-08-16T14:00:00Z",
  missed_fires: 0,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockSave.mockReturnValue({ mutate: save, isPending: false } as never);
  mockPause.mockReturnValue({ mutate: pause, isPending: false } as never);
  mockConnections.mockReturnValue(query(undefined));
});

afterEach(cleanup);

function renderHandler(over: Partial<ScriptContract> = {}, s: ScriptSchedule | null = schedule, onDone?: () => void) {
  return render(
    <ScheduleHandler scriptId="script-001" contract={{ ...contract, ...over }} schedule={s} onDone={onDone} />,
  );
}

describe("ScheduleHandler: what is in force", () => {
  it("states the cadence in words, with the expression under it", () => {
    renderHandler();
    expect(screen.getByText(/Every weekday at 7:00 AM, America\/Los_Angeles/)).toBeInTheDocument();
    expect(screen.getAllByText("0 7 * * 1-5").length).toBeGreaterThan(0);
    expect(screen.getByText(/next fire/)).toBeInTheDocument();
  });

  it("prefills the builder from the schedule in force, bindings included", () => {
    renderHandler();
    expect(screen.getByRole("button", { name: "Weekdays" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByLabelText("Time")).toHaveValue("07:00");
    expect(screen.getByLabelText("Timezone")).toHaveValue("America/Los_Angeles");
    expect(screen.getByLabelText("report_date")).toHaveValue("${fire_date}");
  });

  it("acts on the schedule it shows, by its id", () => {
    renderHandler();
    // A script holds several schedules (#2434): each saves and pauses itself.
    expect(mockSave).toHaveBeenCalledWith("script-001", "sched-001");
    expect(mockPause).toHaveBeenCalledWith("sched-001");
  });

  // A gap is the one thing a schedule cannot report by simply existing.
  it("reports missed fires and that they are never caught up on", () => {
    renderHandler({}, { ...schedule, missed_fires: 3 });
    expect(screen.getByText(/3 fires have been missed/)).toBeInTheDocument();
    expect(screen.getByText(/never caught up on/)).toBeInTheDocument();
  });

  it("names one missed fire in the singular", () => {
    renderHandler({}, { ...schedule, missed_fires: 1 });
    expect(screen.getByText(/1 fire has been missed/)).toBeInTheDocument();
  });

  // An expression whose last fire has passed is a real state; a dash beside
  // "next fire" would read as a page that failed to load something.
  it("says an enabled schedule has nothing further due", () => {
    renderHandler({}, { ...schedule, next_run_at: undefined });
    expect(screen.getByText(/enabled, with no further fire due/)).toBeInTheDocument();
  });

  it("says a paused schedule is firing nothing", () => {
    renderHandler({}, { ...schedule, enabled: false });
    expect(screen.getByText(/paused, and firing nothing/)).toBeInTheDocument();
  });
});

describe("ScheduleHandler: a new schedule", () => {
  // Adding a schedule starts from the builder's default in the reader's zone,
  // with nothing to pause yet.
  it("offers Add schedule and Cancel, and nothing to pause", () => {
    const onDone = vi.fn();
    renderHandler({}, null, onDone);
    expect(mockSave).toHaveBeenCalledWith("script-001", undefined);
    expect(screen.getByRole("button", { name: "Add schedule" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Update schedule" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Pause" })).not.toBeInTheDocument();
    expect(screen.queryByText(/next fire/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onDone).toHaveBeenCalled();
    expect(save).not.toHaveBeenCalled();
  });

  it("adds a schedule handler of the cadence chosen, and says it is done once it is saved", () => {
    const onDone = vi.fn();
    renderHandler({}, null, onDone);
    fireEvent.click(screen.getByRole("button", { name: "Daily" }));
    fireEvent.change(screen.getByLabelText("Time"), { target: { value: "06:30" } });
    fireEvent.change(screen.getByLabelText("Timezone"), { target: { value: "UTC" } });
    fireEvent.change(screen.getByLabelText("report_date"), { target: { value: "${fire_date}" } });
    fireEvent.click(screen.getByRole("button", { name: "Add schedule" }));
    expect(save).toHaveBeenCalledWith(
      { kind: "schedule", cron: "30 6 * * *", timezone: "UTC", args: { report_date: "${fire_date}" } },
      expect.anything(),
    );
    expect(onDone).not.toHaveBeenCalled();
    act(() => (save.mock.calls[0]![1] as { onSuccess: () => void }).onSuccess());
    expect(onDone).toHaveBeenCalled();
  });

  // The builder always means something, so the only way to submit nothing is
  // to empty the advanced field.
  it("cannot be submitted with an empty custom expression", () => {
    renderHandler({}, null, vi.fn());
    fireEvent.click(screen.getByRole("button", { name: "Custom" }));
    fireEvent.change(screen.getByLabelText("Cron expression"), { target: { value: "" } });
    expect(screen.getByRole("button", { name: "Add schedule" })).toBeDisabled();
  });
});

describe("ScheduleHandler: changing the cadence", () => {
  // The builder is the input and cron is the wire format: what the page
  // submits is derived from the choices, never typed by the person making them.
  it("submits the expression its choices mean, with the bindings every fire passes", () => {
    renderHandler();
    fireEvent.change(screen.getByLabelText("Time"), { target: { value: "06:30" } });
    fireEvent.change(screen.getByLabelText("Timezone"), { target: { value: "UTC" } });
    fireEvent.click(screen.getByRole("button", { name: "Update schedule" }));

    expect(save).toHaveBeenCalledWith(
      { kind: "schedule", cron: "30 6 * * 1-5", timezone: "UTC", args: { report_date: "${fire_date}" } },
      expect.anything(),
    );
  });

  it("submits a whole different cadence when the kind changes", () => {
    renderHandler();
    fireEvent.click(screen.getByRole("button", { name: "Daily" }));
    fireEvent.click(screen.getByRole("button", { name: "Update schedule" }));

    expect(save).toHaveBeenCalledWith(expect.objectContaining({ cron: "0 7 * * *" }), expect.anything());
  });

  // An empty box is an unbound parameter, not an empty value: "" would be
  // refused for a date, and the contract's own refusal is the better answer.
  it("omits a parameter left empty rather than binding an empty value", () => {
    renderHandler({}, { ...schedule, params: {} });
    fireEvent.click(screen.getByRole("button", { name: "Update schedule" }));
    expect(save).toHaveBeenCalledWith(
      { kind: "schedule", cron: "0 7 * * 1-5", timezone: "America/Los_Angeles", args: {} },
      expect.anything(),
    );
  });

  it("reports a refused change in place, in the server's words", () => {
    renderHandler();
    fireEvent.click(screen.getByRole("button", { name: "Update schedule" }));
    const onError = save.mock.calls[0]![1].onError as (e: unknown) => void;
    act(() => onError(new Error('the cadence "every tuesday" is not a cron expression')));
    expect(screen.getByText(/not a cron expression/)).toBeInTheDocument();
  });

  it("refuses a duplicate in the server's words", () => {
    renderHandler({}, null, vi.fn());
    fireEvent.click(screen.getByRole("button", { name: "Add schedule" }));
    const onError = save.mock.calls[0]![1].onError as (e: unknown) => void;
    act(() => onError(new Error("script daily-sales-report already has schedule handler sched-001 on 0 7 * * 1-5")));
    expect(screen.getByText(/already has schedule handler sched-001/)).toBeInTheDocument();
  });

  it("pauses and resumes without touching the cadence", () => {
    renderHandler();
    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    expect(pause).toHaveBeenCalledWith(false, expect.anything());
    expect(save).not.toHaveBeenCalled();

    cleanup();
    renderHandler({}, { ...schedule, enabled: false });
    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    expect(pause).toHaveBeenCalledWith(true, expect.anything());
  });

  it("reports a refused pause in place", () => {
    renderHandler();
    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    const onError = pause.mock.calls[0]![1].onError as (e: unknown) => void;
    act(() => onError(new Error("the handler store failed")));
    expect(screen.getByText("the handler store failed")).toBeInTheDocument();
  });

  it("disables every control while a change is in flight", () => {
    mockSave.mockReturnValue({ mutate: save, isPending: true } as never);
    renderHandler();
    expect(screen.getByLabelText("Time")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Update schedule" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Pause" })).toBeDisabled();
  });
});

describe("ScheduleHandler: a script nothing will execute", () => {
  const refused: Partial<ScriptContract> = {
    enabled: false,
    refusal: "the script is disabled, so a run would be refused",
  };

  // The notice is driven by the run gate's own refusal, carried on the
  // contract: a cadence on a disabled or retired script saves and stays inert,
  // and the page says so rather than implying a fire it cannot produce.
  it("says a saved schedule will execute nothing, and still lets it be saved", () => {
    renderHandler(refused);
    expect(screen.getByText(/nothing will execute it/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Update schedule" })).toBeEnabled();
  });

  it("says the same before there is a schedule at all", () => {
    renderHandler(refused, null, vi.fn());
    expect(screen.getByText(/saves, and stays inert/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add schedule" })).toBeEnabled();
  });

  it("says nothing of the sort when the gate admits a run", () => {
    renderHandler();
    expect(screen.queryByText(/nothing will execute it/)).not.toBeInTheDocument();
  });
});

// The headline is what the Handlers section's row states in place of the
// builder, in the words the listing uses.
describe("scheduleHeadline", () => {
  it("says what a schedule does, that a paused one does nothing, and when there is none", () => {
    expect(scheduleHeadline(schedule)).toBe("Runs: Every weekday at 7:00 AM, America/Los_Angeles");
    expect(scheduleHeadline({ ...schedule, enabled: false })).toBe(
      "Paused: Every weekday at 7:00 AM, America/Los_Angeles",
    );
    expect(scheduleHeadline(null)).toBe("Not scheduled");
  });
});
