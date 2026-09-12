import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ToolHintBadges } from "./ToolHintBadges";

afterEach(cleanup);

// Each case is one shape tools/list serves (#1706). The unstated cases follow
// the MCP defaults, which is what a client that honors the hints will do.
describe("ToolHintBadges", () => {
  it("marks a read as read-only and idempotent, and not destructive", () => {
    render(<ToolHintBadges annotations={{ readOnlyHint: true, idempotentHint: true }} />);
    expect(screen.getByText("read-only")).toBeInTheDocument();
    expect(screen.getByText("idempotent")).toBeInTheDocument();
    expect(screen.queryByText("destructive")).toBeNull();
  });

  it("marks a write that states destructiveHint false as additive", () => {
    render(
      <ToolHintBadges
        annotations={{ readOnlyHint: false, destructiveHint: false, idempotentHint: false }}
      />,
    );
    expect(screen.getByText("additive")).toBeInTheDocument();
    expect(screen.queryByText("destructive")).toBeNull();
    expect(screen.queryByText("idempotent")).toBeNull();
  });

  it("marks a write that states destructiveHint true as destructive", () => {
    render(
      <ToolHintBadges
        annotations={{ readOnlyHint: false, destructiveHint: true, idempotentHint: true }}
      />,
    );
    expect(screen.getByText("destructive")).toHaveAttribute(
      "title",
      expect.stringContaining("may remove or overwrite"),
    );
    expect(screen.getByText("idempotent")).toBeInTheDocument();
  });

  it("marks a write that leaves destructiveHint out as destructive, saying it is unstated", () => {
    render(<ToolHintBadges annotations={{ readOnlyHint: false, idempotentHint: false }} />);
    expect(screen.getByText("destructive")).toHaveAttribute(
      "title",
      expect.stringContaining("not stated"),
    );
  });

  it("says a tool with no annotations advertises no hints", () => {
    render(<ToolHintBadges />);
    expect(screen.getByText("no hints")).toBeInTheDocument();
    expect(screen.queryByText("read-only")).toBeNull();
  });
});
