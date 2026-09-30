import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ScriptVersion } from "@/api/admin/types";
import { ScriptVersionCompare } from "./ScriptVersionCompare";

vi.mock("@/api/portal/hooks/scriptRuns", async (orig) => ({
  ...(await orig<typeof import("@/api/portal/hooks/scriptRuns")>()),
  useScriptRuns: () => ({ data: undefined }),
  useRecentScriptRuns: () => ({ data: undefined }),
}));
vi.mock("@/api/portal/hooks/scriptFlow", () => ({
  useScriptFlow: vi.fn(() => ({ isLoading: true })),
  useScriptRunFlow: () => ({ isLoading: false }),
}));
import { useScriptFlow } from "@/api/portal/hooks/scriptFlow";

afterEach(cleanup);

const version = (n: number, source: string): ScriptVersion =>
  ({ id: `v${n}`, script_id: "s1", version: n, source, status: "applied", author: "a", created_at: "" }) as ScriptVersion;

describe("ScriptVersionCompare", () => {
  it("opens on the newer version's diagram compared with the older, and offers the text diff", () => {
    const onClose = vi.fn();
    render(
      <ScriptVersionCompare
        scriptId="s1"
        from={version(1, "a = 1\nb = 2\n")}
        to={version(3, "a = 1\nb = 3\nc = 4\n")}
        onClose={onClose}
      />,
    );
    expect(screen.getByText("v1 → v3")).toBeInTheDocument();
    expect(screen.getByText("+2")).toBeInTheDocument();
    expect(screen.getByText("-1")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Flow" })).toHaveAttribute("aria-selected", "true");
    expect(vi.mocked(useScriptFlow)).toHaveBeenCalledWith("s1", 3, 1);

    fireEvent.mouseDown(screen.getByRole("tab", { name: "Text" }));
    expect(screen.getByText("b = 3")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Close comparison" }));
    expect(onClose).toHaveBeenCalled();
  });
});
