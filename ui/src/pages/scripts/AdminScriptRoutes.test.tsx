import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { AdminScriptRoutes } from "./AdminScriptRoutes";

// The section's own route matching, driven by the addresses the shell hands it.
// Both pages are stubbed: what is under test is which page an address opens and
// what it is told, not what either page renders.
vi.mock("./AdminScriptsPage", () => ({
  AdminScriptsPage: () => <div data-testid="listing" />,
}));

vi.mock("./ScriptDetailPage", () => ({
  ScriptDetailPage: ({
    scriptId,
    openRunId,
    backLabel,
  }: {
    scriptId: string;
    openRunId?: string;
    backLabel?: string;
  }) => (
    <div
      data-testid="detail"
      data-script={scriptId}
      data-run={openRunId ?? ""}
      data-back={backLabel ?? ""}
    />
  ),
}));

const onNavigate = vi.fn();

afterEach(cleanup);

describe("AdminScriptRoutes", () => {
  it("opens the listing at the section index", () => {
    render(<AdminScriptRoutes route="/admin/scripts" onNavigate={onNavigate} />);
    expect(screen.getByTestId("listing")).toBeInTheDocument();
  });

  it("opens one script on the same page its owner opens", () => {
    render(<AdminScriptRoutes route="/admin/scripts/script-001" onNavigate={onNavigate} />);
    const detail = screen.getByTestId("detail");
    expect(detail).toHaveAttribute("data-script", "script-001");
    expect(detail).toHaveAttribute("data-run", "");
    // The way back is the section the reader came from, not the owner's.
    expect(detail).toHaveAttribute("data-back", "All scripts");
  });

  // #1407: a run in the operator's listing opens its script's page with that
  // run already open, which is what the owner's section does too.
  it("opens one run on its script's page", () => {
    render(
      <AdminScriptRoutes route="/admin/scripts/script-001/runs/run-042" onNavigate={onNavigate} />,
    );
    const detail = screen.getByTestId("detail");
    expect(detail).toHaveAttribute("data-script", "script-001");
    expect(detail).toHaveAttribute("data-run", "run-042");
  });

  // A path under a script that names no page is not a script whose id contains
  // a slash: the shell answers it with the not-found page.
  it("renders nothing for a path under a script that names no page", () => {
    const { container } = render(
      <AdminScriptRoutes route="/admin/scripts/script-001/nonesuch" onNavigate={onNavigate} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
