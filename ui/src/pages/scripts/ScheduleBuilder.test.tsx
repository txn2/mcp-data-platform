import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { ScheduleBuilder } from "./ScheduleBuilder";
import { fromCron, type Cadence } from "./cadence";

// The builder exists so nobody has to know cron, so every test here asks the
// question a person would: I picked this, what did the platform get?

const onCadenceChange = vi.fn();
const onTimezoneChange = vi.fn();

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

function renderBuilder(cadence: Cadence, timezone = "UTC", saved: { cron: string; timezone: string } | null = null) {
  render(
    <ScheduleBuilder
      cadence={cadence}
      timezone={timezone}
      busy={false}
      saved={saved}
      onCadenceChange={onCadenceChange}
      onTimezoneChange={onTimezoneChange}
    />,
  );
}

const weekdays: Cadence = { kind: "weekdays", hour: 7, minute: 0 };

describe("ScheduleBuilder: choosing a cadence", () => {
  it("shows the cadence in force as the pressed choice", () => {
    renderBuilder(weekdays);
    expect(screen.getByRole("button", { name: "Weekdays" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: "Daily" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });

  // Somebody who has set 7:00 AM and then changes their mind about which days
  // has not changed their mind about the time.
  it("carries the time of day across a change of kind", () => {
    renderBuilder({ kind: "daily", hour: 6, minute: 30 });
    fireEvent.click(screen.getByRole("button", { name: "Weekly" }));
    expect(onCadenceChange).toHaveBeenCalledWith({
      kind: "weekly",
      days: [1],
      hour: 6,
      minute: 30,
    });
  });

  it("hands the current expression to the custom field rather than emptying it", () => {
    renderBuilder(weekdays);
    fireEvent.click(screen.getByRole("button", { name: "Custom" }));
    expect(onCadenceChange).toHaveBeenCalledWith({ kind: "custom", spec: "0 7 * * 1-5" });
  });

  it("asks for the minute, not the hour, on an hourly cadence", () => {
    renderBuilder({ kind: "hourly", minute: 15 });
    expect(screen.getByLabelText("Minutes past the hour")).toHaveValue(15);
    expect(screen.queryByLabelText("Time")).not.toBeInTheDocument();
  });

  it("asks for a day of the month on a monthly cadence", () => {
    renderBuilder({ kind: "monthly", day: 3, hour: 6, minute: 0 });
    expect(screen.getByLabelText("Day of the month")).toHaveValue(3);
  });
});

describe("ScheduleBuilder: the time of day", () => {
  it("is a clock, not two cron fields", () => {
    renderBuilder({ kind: "daily", hour: 13, minute: 5 });
    expect(screen.getByLabelText("Time")).toHaveValue("13:05");
  });

  it("reports a new time as hour and minute", () => {
    renderBuilder(weekdays);
    fireEvent.change(screen.getByLabelText("Time"), { target: { value: "18:45" } });
    expect(onCadenceChange).toHaveBeenCalledWith({ kind: "weekdays", hour: 18, minute: 45 });
  });

  // A browser that hands back something unparseable must not silently move the
  // schedule to midnight.
  it("ignores a time it cannot read", () => {
    renderBuilder(weekdays);
    fireEvent.change(screen.getByLabelText("Time"), { target: { value: "" } });
    expect(onCadenceChange).not.toHaveBeenCalled();
  });
});

describe("ScheduleBuilder: days of the week", () => {
  const weekly: Cadence = { kind: "weekly", days: [1, 4], hour: 8, minute: 0 };

  it("shows which days are chosen", () => {
    renderBuilder(weekly);
    expect(screen.getByRole("button", { name: "Monday" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Tuesday" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });

  it("adds and removes a day", () => {
    renderBuilder(weekly);
    fireEvent.click(screen.getByRole("button", { name: "Saturday" }));
    expect(onCadenceChange).toHaveBeenCalledWith({ ...weekly, days: [1, 4, 6] });

    fireEvent.click(screen.getByRole("button", { name: "Monday" }));
    expect(onCadenceChange).toHaveBeenCalledWith({ ...weekly, days: [4] });
  });

  it("says plainly that no day chosen means every day", () => {
    renderBuilder({ kind: "weekly", days: [], hour: 8, minute: 0 });
    expect(screen.getByText(/would run every day/)).toBeInTheDocument();
  });
});

describe("ScheduleBuilder: what it will save", () => {
  it("states the choice in words and shows the expression it produces", () => {
    renderBuilder(weekdays, "America/Los_Angeles");
    expect(
      screen.getByText(/Every weekday at 7:00 AM, America\/Los_Angeles/),
    ).toBeInTheDocument();
    expect(screen.getByText("0 7 * * 1-5")).toBeInTheDocument();
  });

  // Unchanged, the sentence would only repeat what the card above already says.
  it("says nothing while the choices match the schedule in force", () => {
    renderBuilder(weekdays, "UTC", { cron: "0 7 * * 1-5", timezone: "UTC" });
    expect(screen.queryByText(/Every weekday at 7:00 AM/)).not.toBeInTheDocument();
  });

  it("appears as soon as the choices differ", () => {
    renderBuilder(weekdays, "UTC", { cron: "0 9 * * 1-5", timezone: "UTC" });
    expect(screen.getByText(/Saves as:/)).toBeInTheDocument();
  });

  // A cadence on the 31st is a real choice with a real consequence, and the
  // platform skips the months that do not have one rather than moving the run.
  it("warns about months without the chosen day", () => {
    renderBuilder({ kind: "monthly", day: 31, hour: 6, minute: 0 });
    expect(screen.getByText(/Months without that day are skipped/)).toBeInTheDocument();
  });

  it("does not show a derived expression for a custom cadence, which is one", () => {
    renderBuilder({ kind: "custom", spec: "*/30 * * * *" });
    expect(screen.getByLabelText("Cron expression")).toHaveValue("*/30 * * * *");
    expect(screen.getByText(/Custom schedule: \*\/30 \* \* \* \*/)).toBeInTheDocument();
  });
});

describe("ScheduleBuilder: the timezone", () => {
  it("offers the zone in force and reports a change", () => {
    renderBuilder(weekdays, "America/Los_Angeles");
    const select = screen.getByLabelText("Timezone");
    expect(select).toHaveValue("America/Los_Angeles");

    fireEvent.change(select, { target: { value: "UTC" } });
    expect(onTimezoneChange).toHaveBeenCalledWith("UTC");
  });
});

describe("ScheduleBuilder: round trip", () => {
  // Everything the builder can produce has to read back as the same choice, or
  // reopening a schedule would silently re-time it.
  it("produces expressions it can read back", () => {
    const cases: Cadence[] = [
      { kind: "hourly", minute: 0 },
      { kind: "daily", hour: 7, minute: 0 },
      weekdays,
      { kind: "weekly", days: [0, 3], hour: 22, minute: 15 },
      { kind: "monthly", day: 28, hour: 4, minute: 5 },
    ];
    for (const c of cases) {
      cleanup();
      vi.clearAllMocks();
      renderBuilder(c);
      const shown = screen.getAllByText(/^[\d*,/ -]+$/).map((el) => el.textContent ?? "");
      const spec = shown.find((t) => t.split(/\s+/).length === 5);
      expect(spec, `no expression rendered for ${c.kind}`).toBeTruthy();
      expect(fromCron(spec!)).toEqual(c);
    }
  });
});
