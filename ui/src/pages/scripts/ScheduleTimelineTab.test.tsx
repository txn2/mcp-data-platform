import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import type { ScheduleTimeline } from "@/api/portal/hooks/scheduleTimeline";
import { ScheduleTimelineTab } from "./ScheduleTimelineTab";

vi.mock("@/api/portal/hooks/scheduleTimeline", async (importActual) => ({
  ...(await importActual<object>()),
  useScheduleTimeline: vi.fn(),
}));

import { useScheduleTimeline } from "@/api/portal/hooks/scheduleTimeline";

const mockTimeline = vi.mocked(useScheduleTimeline);
const onNavigate = vi.fn();
const onShowScripts = vi.fn();

const DAY = 86_400_000;
const dayFrom = Date.parse("2026-01-14T00:00:00Z");

function every(stepMs: number, count: number, start = dayFrom): string[] {
  return Array.from({ length: count }, (_, i) =>
    new Date(start + i * stepMs).toISOString(),
  );
}

// timeline is the ticket's own four schedules, plus a paused one, as the
// server lays them out for a UTC viewer.
function timeline(): ScheduleTimeline {
  return {
    timezone: "UTC",
    unreadable: [],
    sections: [
      {
        section: "intraday",
        from: new Date(dayFrom).toISOString(),
        to: new Date(dayFrom + DAY).toISOString(),
        rows: [
          {
            script_id: "s-5m",
            script_name: "orders-ingest",
            cron_spec: "*/5 * * * *",
            timezone: "UTC",
            enabled: true,
            rhythm: "minutes",
            fire_count: 288,
            truncated: false,
            fires: every(300_000, 288),
          },
          {
            script_id: "s-35",
            script_name: "hourly-rollup",
            cron_spec: "35 * * * *",
            timezone: "UTC",
            enabled: false,
            rhythm: "hours",
            fire_count: 24,
            truncated: false,
            fires: every(3_600_000, 24, dayFrom + 35 * 60_000),
          },
        ],
      },
      {
        section: "multi_day",
        from: "2026-01-12T00:00:00Z",
        to: "2026-01-19T00:00:00Z",
        rows: [
          {
            script_id: "s-wd",
            script_name: "morning-report",
            cron_spec: "0 7 * * 1-5",
            timezone: "America/New_York",
            enabled: true,
            rhythm: "days",
            fire_count: 5,
            truncated: false,
            fires: every(DAY, 5, Date.parse("2026-01-12T12:00:00Z")),
          },
        ],
      },
      {
        section: "long_term",
        from: "2026-01-01T00:00:00Z",
        to: "2026-04-01T00:00:00Z",
        rows: [],
      },
    ],
  };
}

function answer(data: ScheduleTimeline | undefined, extra: object = {}) {
  mockTimeline.mockReturnValue({
    data,
    isLoading: false,
    error: null,
    ...extra,
  } as never);
}

function renderTab(basePath = "/automations") {
  render(
    <ScheduleTimelineTab
      basePath={basePath}
      onNavigate={onNavigate}
      onShowScripts={onShowScripts}
    />,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  answer(timeline());
});
afterEach(cleanup);

describe("ScheduleTimelineTab", () => {
  it("draws each section that has a schedule, and not the one that has none", () => {
    renderTab();
    expect(screen.getByText("Intraday")).toBeInTheDocument();
    expect(screen.getByText("Multi-day")).toBeInTheDocument();
    expect(screen.queryByText("Long-term")).not.toBeInTheDocument();
  });

  it("marks every fire the server listed", () => {
    renderTab();
    const marks = (id: string) =>
      within(screen.getByTestId(`schedule-row-${id}`)).getByTestId(
        "schedule-marks",
      );
    expect(marks("s-5m").querySelectorAll("line")).toHaveLength(288);
    expect(marks("s-35").querySelectorAll("line")).toHaveLength(24);
    // Five evenly spaced fires are a cadence, drawn as ticks.
    expect(marks("s-wd").querySelectorAll("line")).toHaveLength(5);
    expect(
      marks("s-5m").querySelector("line")!.getAttribute("stroke-width"),
    ).toBe("1.3");
  });

  it("draws a row the server cut at its cap as one band to its last fire (#1933)", () => {
    const data = timeline();
    data.sections[0]!.rows.push({
      script_id: "s-1m",
      script_name: "revenue-pulse",
      cron_spec: "* * * * *",
      timezone: "UTC",
      enabled: false,
      rhythm: "minutes",
      fire_count: 1440,
      truncated: true,
      fires: every(60_000, 500),
      last_fire: new Date(dayFrom + DAY - 60_000).toISOString(),
    });
    answer(data);
    renderTab();
    const marks = within(screen.getByTestId("schedule-row-s-1m")).getByTestId(
      "schedule-marks",
    );
    expect(marks.querySelectorAll("line")).toHaveLength(0);
    const band = within(marks).getByTestId("schedule-band");
    const reach =
      Number(band.getAttribute("x")) + Number(band.getAttribute("width"));
    // Every row's hit target spans the whole window; the band reaches within
    // a minute of its end, not a third of the way across.
    const whole = Number(
      screen
        .getByTestId("schedule-row-s-1m")
        .querySelector("rect.row-hit")!
        .getAttribute("width"),
    );
    expect(reach).toBeGreaterThan(whole * 0.95);
    // An uncut row is still drawn fire by fire.
    expect(
      within(screen.getByTestId("schedule-row-s-5m")).queryByTestId(
        "schedule-band",
      ),
    ).toBeNull();
  });

  it("labels a row with its schedule in words and its count", () => {
    renderTab();
    expect(screen.getByText("Every 5 minutes · 288 a day")).toBeInTheDocument();
    expect(
      screen.getByText("Every weekday at 7:00 AM, New York time · 5 a week"),
    ).toBeInTheDocument();
  });

  it("draws a paused schedule muted, labeled paused, with its marks", () => {
    renderTab();
    const row = screen.getByTestId("schedule-row-s-35");
    expect(
      within(row).getByText(/· paused$/, { selector: "text" }),
    ).toBeInTheDocument();
    const marks = within(row).getByTestId("schedule-marks");
    expect(marks.getAttribute("opacity")).toBe("0.5");
    expect(marks.querySelector("line")!.getAttribute("stroke")).toBe(
      "hsl(var(--muted-foreground))",
    );
    expect(screen.getByText("Paused")).toBeInTheDocument();
  });

  it("opens the script when a row is clicked, under the page's own section", () => {
    renderTab("/admin/automations");
    fireEvent.click(screen.getByTestId("schedule-row-s-wd"));
    expect(onNavigate).toHaveBeenCalledWith("/admin/automations/s-wd");
  });

  it("opens the script when its name is clicked, which reads as a link", () => {
    renderTab();
    const name = within(screen.getByTestId("schedule-row-s-5m")).getByTestId(
      "schedule-row-name",
    );
    expect(name.getAttribute("fill")).toBe("hsl(var(--primary))");
    fireEvent.click(name);
    expect(onNavigate).toHaveBeenCalledWith("/automations/s-5m");
  });

  it("opens the script from the keyboard", () => {
    renderTab();
    fireEvent.keyDown(screen.getByTestId("schedule-row-s-5m"), {
      key: "Enter",
    });
    expect(onNavigate).toHaveBeenCalledWith("/automations/s-5m");
  });

  it("states a focused fire in the viewer's zone and the schedule's own", () => {
    renderTab();
    const row = screen.getByTestId("schedule-row-s-wd");
    fireEvent.focus(row);
    let tip = screen.getByRole("tooltip");
    expect(tip).toHaveTextContent("morning-report");
    expect(tip).toHaveTextContent(/12:00.*your time/);
    expect(tip).toHaveTextContent(/7:00.*America\/New_York/);

    fireEvent.keyDown(row, { key: "ArrowRight" });
    tip = screen.getByRole("tooltip");
    expect(tip).toHaveTextContent(/Tue/);

    fireEvent.keyDown(row, { key: "Escape" });
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
  });

  it("draws a few irregular fires as dots", () => {
    const data = timeline();
    data.sections[2]!.rows = [
      {
        script_id: "s-m",
        script_name: "quarter-close",
        cron_spec: "0 6 1 * *",
        timezone: "UTC",
        enabled: true,
        rhythm: "months",
        fire_count: 3,
        truncated: false,
        fires: [
          "2026-01-01T06:00:00Z",
          "2026-02-01T06:00:00Z",
          "2026-03-01T06:00:00Z",
        ],
      },
    ];
    answer(data);
    renderTab();
    expect(screen.getByText("Long-term")).toBeInTheDocument();
    const marks = within(screen.getByTestId("schedule-row-s-m")).getByTestId(
      "schedule-marks",
    );
    expect(marks.querySelectorAll("circle")).toHaveLength(3);
  });

  it("shows an empty state that leads to the Automations tab, not three empty axes", () => {
    const data = timeline();
    for (const s of data.sections) s.rows = [];
    answer(data);
    renderTab();
    expect(screen.queryByText("Intraday")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Go to Automations" }));
    expect(onShowScripts).toHaveBeenCalled();
  });

  it("names a schedule the server could not read", () => {
    const data = timeline();
    data.unreadable = [
      { script_id: "s-x", script_name: "broken", reason: "bad cron" },
    ];
    answer(data);
    renderTab();
    expect(
      screen.getByText(/One schedule could not be read.*broken \(bad cron\)/),
    ).toBeInTheDocument();
  });

  it("says when it is loading and when it failed", () => {
    answer(undefined, { isLoading: true });
    renderTab();
    expect(screen.getByText("Loading schedules...")).toBeInTheDocument();
    cleanup();
    answer(undefined, { error: new Error("boom") });
    renderTab();
    expect(
      screen.getByText("The schedules could not be loaded."),
    ).toBeInTheDocument();
  });
});
