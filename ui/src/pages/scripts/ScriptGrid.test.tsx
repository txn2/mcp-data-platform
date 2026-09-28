import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { ScriptGrid, scriptTileSrc } from "./ScriptGrid";

afterEach(cleanup);

describe("scriptTileSrc", () => {
  it("addresses a script's tile by the version it should show, light or dark", () => {
    expect(scriptTileSrc("s1", 4, false)).toBe("/api/v1/portal/scripts/s1/thumbnail?v=4");
    expect(scriptTileSrc("s1", 4, true)).toBe("/api/v1/portal/scripts/s1/thumbnail?v=4&variant=dark");
  });
});

// card is one grid row: a script the caller owns, with no schedule and no run.
function card(id: string, library: boolean): PortalScriptRow {
  return {
    script: {
      id,
      name: id,
      display_name: id,
      description: "",
      owner_email: "sarah.chen@example.com",
      status: "active",
      enabled: true,
      version: 1,
      library,
      loads: [],
      updated_at: "2026-08-14T09:00:00Z",
    },
    owned: true,
  };
}

describe("ScriptGrid", () => {
  // A library (#1941) is never run or scheduled, so its card is badged as one
  // and promises neither.
  it("badges a library's card and no other, and promises a library no run", () => {
    render(
      <ScriptGrid
        rows={[card("date-windows", true), card("daily-sales", false)]}
        basePath="/automations"
        onNavigate={vi.fn()}
      />,
    );

    const lib = within(screen.getByTestId("script-card-date-windows"));
    expect(lib.getByText("Library")).toBeInTheDocument();
    expect(lib.queryByText("On demand")).not.toBeInTheDocument();
    expect(lib.queryByText("Never run")).not.toBeInTheDocument();

    const automation = within(screen.getByTestId("script-card-daily-sales"));
    expect(automation.queryByText("Library")).not.toBeInTheDocument();
    expect(automation.getByText("On demand")).toBeInTheDocument();
    expect(automation.getByText("Never run")).toBeInTheDocument();
  });
});
