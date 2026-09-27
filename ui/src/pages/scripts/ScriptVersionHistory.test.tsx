import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { ScriptVersionHistory } from "./ScriptVersionHistory";

vi.mock("@/api/portal/hooks/scripts", () => ({
  usePortalScriptVersions: () => ({
    isLoading: false,
    error: null,
    data: {
      data: [
        { id: "v3", script_id: "s1", version: 3, source: "x = 3\n", status: "applied", author: "a", created_at: "" },
        { id: "v2", script_id: "s1", version: 2, source: "x = 2\n", status: "superseded", author: "a", created_at: "" },
      ],
      total: 2,
    },
  }),
}));
vi.mock("@/api/portal/hooks/scriptRuns", async (orig) => ({
  ...(await orig<typeof import("@/api/portal/hooks/scriptRuns")>()),
  useScriptRuns: () => ({ data: undefined }),
}));
vi.mock("@/api/portal/hooks/scriptFlow", () => ({
  useScriptFlow: () => ({ isLoading: true }),
  useScriptRunFlow: () => ({ isLoading: false }),
}));

afterEach(cleanup);

const contract = { id: "s1", name: "s", version: 3 } as ScriptContract;

describe("ScriptVersionHistory: comparing (#1908)", () => {
  it("offers each older version a comparison with the version that runs", () => {
    render(<ScriptVersionHistory scriptId="s1" contract={contract} />);
    fireEvent.click(screen.getByRole("button", { name: /Version history/ }));
    const compare = screen.getAllByRole("button", { name: /^Compare with/ });
    expect(compare).toHaveLength(1);
    expect(compare[0]).toHaveTextContent("Compare with v3");

    fireEvent.click(compare[0]!);
    expect(screen.getByTestId("version-compare")).toHaveTextContent("v2 → v3");
    // The compare control does not also open or close the version's source.
    expect(screen.queryByText("x = 2")).not.toBeInTheDocument();
    fireEvent.click(compare[0]!);
    expect(screen.queryByTestId("version-compare")).not.toBeInTheDocument();
  });
});
