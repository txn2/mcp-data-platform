import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import {
  AssetRetentionField,
  retentionCountValid,
  retentionModeFor,
  retentionUnchanged,
  retentionValue,
  type RetentionMode,
} from "./AssetRetentionField";

// The retention control is the person-facing half of #1421. What is asserted
// here is the round trip: a stored value picks the mode that describes it, the
// mode a person picks resolves back to the value the API expects, and the count
// field appears only in the mode that needs one.

afterEach(cleanup);

describe("retentionModeFor", () => {
  it("reads an absent override as the deployment default", () => {
    expect(retentionModeFor(undefined)).toBe("default");
    expect(retentionModeFor(null)).toBe("default");
  });

  it("reads 0 as unlimited rather than as a cap of nothing", () => {
    expect(retentionModeFor(0)).toBe("unlimited");
  });

  it("reads a positive number as a custom cap", () => {
    expect(retentionModeFor(1)).toBe("custom");
    expect(retentionModeFor(250)).toBe("custom");
  });
});

describe("retentionValue", () => {
  it("sends null for the deployment default, which clears the override", () => {
    expect(retentionValue("default", "50")).toBeNull();
  });

  it("sends 0 for unlimited", () => {
    expect(retentionValue("unlimited", "50")).toBe(0);
  });

  it("sends the typed count", () => {
    expect(retentionValue("custom", "50")).toBe(50);
  });

  it("falls back to the deployment default for an unfinished count", () => {
    // Neither guess is safe from a blank box: 0 means "keep everything" and 1
    // means "keep almost nothing", so an unresolved cap inherits instead. Save
    // is disabled in this state, so this is the backstop, not the path.
    expect(retentionValue("custom", "")).toBeNull();
    expect(retentionValue("custom", "0")).toBeNull();
    expect(retentionValue("custom", "-5")).toBeNull();
    expect(retentionValue("custom", "abc")).toBeNull();
  });
});

describe("retentionUnchanged", () => {
  it("reads an absent override and the default mode as the same thing", () => {
    expect(retentionUnchanged("default", "", undefined)).toBe(true);
    expect(retentionUnchanged("default", "", null)).toBe(true);
  });

  it("reads a matching count as unchanged and a different one as moved", () => {
    expect(retentionUnchanged("custom", "25", 25)).toBe(true);
    expect(retentionUnchanged("custom", "40", 25)).toBe(false);
  });

  it("distinguishes unlimited from inheriting", () => {
    expect(retentionUnchanged("unlimited", "", 0)).toBe(true);
    expect(retentionUnchanged("unlimited", "", undefined)).toBe(false);
    expect(retentionUnchanged("default", "", 0)).toBe(false);
  });
});

describe("retentionCountValid", () => {
  it("asks nothing of the two modes with no count behind them", () => {
    expect(retentionCountValid("default", "")).toBe(true);
    expect(retentionCountValid("unlimited", "")).toBe(true);
  });

  it("requires a positive count in the custom mode", () => {
    expect(retentionCountValid("custom", "5")).toBe(true);
    expect(retentionCountValid("custom", "")).toBe(false);
    expect(retentionCountValid("custom", "0")).toBe(false);
    expect(retentionCountValid("custom", "-1")).toBe(false);
  });
});

function renderField(mode: RetentionMode, custom = "") {
  const onModeChange = vi.fn();
  const onCustomChange = vi.fn();
  render(
    <AssetRetentionField
      id="t"
      mode={mode}
      custom={custom}
      onModeChange={onModeChange}
      onCustomChange={onCustomChange}
    />,
  );
  return { onModeChange, onCustomChange };
}

describe("AssetRetentionField", () => {
  it("shows the count field only in the custom mode", () => {
    renderField("default");
    expect(screen.queryByLabelText("Versions to keep")).toBeNull();
    cleanup();

    renderField("unlimited");
    expect(screen.queryByLabelText("Versions to keep")).toBeNull();
    cleanup();

    renderField("custom", "25");
    expect(screen.getByLabelText("Versions to keep")).toHaveValue(25);
  });

  it("says what the limit costs, since the content goes with the row", () => {
    renderField("custom", "25");
    expect(screen.getByText(/deleted along with their stored content/)).toBeInTheDocument();
  });

  it("says what is missing when the count is blank", () => {
    renderField("custom", "");
    expect(screen.getByText(/Enter how many versions to keep/)).toBeInTheDocument();
  });

  it("reports a typed count to its owner", () => {
    const { onCustomChange } = renderField("custom", "25");
    fireEvent.change(screen.getByLabelText("Versions to keep"), { target: { value: "7" } });
    expect(onCustomChange).toHaveBeenCalledWith("7");
  });
});
