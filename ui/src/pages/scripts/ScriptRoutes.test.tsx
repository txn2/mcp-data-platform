import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { PortalScriptRoutes } from "./ScriptRoutes";

// The section's own route matching, driven by the addresses the shell hands it.
// Both pages are stubbed: what is under test is which page an address opens and
// what it is told, not what either page renders.
vi.mock("./MyScriptsPage", () => ({
  MyScriptsPage: () => <div data-testid="listing" />,
}));

vi.mock("./ScriptDetailPage", () => ({
  ScriptDetailPage: ({ scriptId, openRunId }: { scriptId: string; openRunId?: string }) => (
    <div data-testid="detail" data-script={scriptId} data-run={openRunId ?? ""} />
  ),
}));

const onNavigate = vi.fn();

afterEach(cleanup);

describe("PortalScriptRoutes", () => {
  it("opens the listing at the section index", () => {
    render(<PortalScriptRoutes route="/scripts" onNavigate={onNavigate} />);
    expect(screen.getByTestId("listing")).toBeInTheDocument();
  });

  it("opens one script, naming no run", () => {
    render(<PortalScriptRoutes route="/scripts/script-001" onNavigate={onNavigate} />);
    const detail = screen.getByTestId("detail");
    expect(detail).toHaveAttribute("data-script", "script-001");
    expect(detail).toHaveAttribute("data-run", "");
  });

  // #1405: a run has no page of its own, so its address opens its script's page
  // with that run already open.
  it("opens one run on its script's page", () => {
    render(<PortalScriptRoutes route="/scripts/script-001/runs/run-042" onNavigate={onNavigate} />);
    const detail = screen.getByTestId("detail");
    expect(detail).toHaveAttribute("data-script", "script-001");
    expect(detail).toHaveAttribute("data-run", "run-042");
  });

  // A path under a script that names no page is not a script whose id contains
  // a slash: the shell answers it with the not-found page.
  it("renders nothing for a path under a script that names no page", () => {
    const { container } = render(
      <PortalScriptRoutes route="/scripts/script-001/nonesuch" onNavigate={onNavigate} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
