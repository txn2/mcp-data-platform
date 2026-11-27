import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";

import { FailuresPanel } from "./FailuresPanel";

const entry = (dimension: string, count: number) => ({
  dimension,
  count,
  success_rate: 0,
  avg_duration_ms: 1,
  hold_count: 0,
});

// #2795: the failed calls of the range, counted by the category each was
// refused with, by where it came from and by persona, every count exact.
describe("the failures panel", () => {
  it("counts the failed calls three ways, in words a person reads", () => {
    render(
      <FailuresPanel
        byCategory={[entry("schema_required", 10212), entry("search_required", 3), entry("brand_new", 1), entry("", 2)]}
        bySource={[entry("mcp", 10213), entry("script", 5)]}
        byPersona={[entry("analyst", 10000), entry("", 218)]}
        isLoading={false}
      />,
    );
    const panel = screen.getByTestId("audit-failures");
    expect(panel.textContent).toContain("10,218 failed calls in this range.");
    expect(within(panel).getByText("Table columns not read first")).toBeTruthy();
    expect(within(panel).getByText("10,212")).toBeTruthy();
    expect(within(panel).getByText("Search not called first")).toBeTruthy();
    expect(within(panel).getByText("brand_new"), "a category not named yet shows as recorded").toBeTruthy();
    expect(within(panel).getByText("No category recorded")).toBeTruthy();
    expect(within(panel).getByText("Agent sessions")).toBeTruthy();
    expect(within(panel).getByText("Script runs")).toBeTruthy();
    expect(within(panel).getByText("analyst")).toBeTruthy();
    expect(within(panel).getByText("No persona")).toBeTruthy();
  });

  it("says so when nothing failed, and waits while loading", () => {
    const { rerender, container } = render(<FailuresPanel byCategory={[]} bySource={[]} byPersona={[]} isLoading={false} />);
    expect(screen.getByText("No failed calls in this range.")).toBeTruthy();
    rerender(<FailuresPanel byCategory={undefined} bySource={[]} byPersona={[]} isLoading />);
    expect(container.querySelector('[data-testid="audit-failures"]')).toBeNull();
  });
});
