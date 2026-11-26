import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { TypecheckCheck } from "@/api/portal/hooks/siteTypecheck";

let check: TypecheckCheck | null;
const mutate = vi.fn();

vi.mock("@/api/portal/hooks/siteTypecheck", async (orig) => ({
  ...(await orig<typeof import("@/api/portal/hooks/siteTypecheck")>()),
  useReleaseTypecheck: () => ({ data: { release: 12, check } }),
  useRetypecheck: () => ({ mutate, isPending: false, error: null }),
}));

import { SiteTypecheckCard, TypecheckBadge, typecheckBadge } from "./SiteTypecheck";

const at = new Date().toISOString();
const base: TypecheckCheck = { id: 1, release: 12, environment: "dev", tsconfig: "tsconfig.json", status: "clean", created_at: at, finished_at: at };
const result = { diagnostics: [], missing: [], unverifiable: [], packages: 0, fetched: 0, duration_ms: 3000 };

describe("SiteTypecheckCard (#2793)", () => {
  beforeEach(() => {
    mutate.mockReset();
    check = null;
  });

  it("shows no card for a release never checked", () => {
    const { container } = render(<SiteTypecheckCard site="marketing" release={12} canCheck />);
    expect(container).toBeEmptyDOMElement();
  });

  it("lists each error with its file, line, column and code, and opens the file", () => {
    const onOpenFile = vi.fn();
    check = {
      ...base,
      status: "errors",
      result: {
        ...result,
        status: "errors",
        packages: 412,
        lockfile: "pnpm-lock.yaml",
        missing: ["date-fns-tz"],
        diagnostics: [{ file: "src/a.ts", line: 4, column: 9, category: "error", code: "TS2322", message: "Type 'string' is not assignable to type 'number'." }],
      },
    };
    render(<SiteTypecheckCard site="marketing" release={12} canCheck onOpenFile={onOpenFile} />);
    expect(screen.getByText("1 error")).toBeInTheDocument();
    expect(screen.getByText("TS2322")).toBeInTheDocument();
    expect(screen.getByText("4")).toBeInTheDocument();
    expect(screen.getByText("9")).toBeInTheDocument();
    expect(screen.getByText(/using the types of 412 packages/)).toBeInTheDocument();
    expect(screen.getByText(/not in the lockfile/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "src/a.ts" }));
    expect(onOpenFile).toHaveBeenCalledWith("src/a.ts");
    fireEvent.click(screen.getByRole("button", { name: /Check again/ }));
    expect(mutate).toHaveBeenCalled();
  });

  it("lists a diagnostic with no file location as a row with no file, line or column (#2834)", () => {
    const onOpenFile = vi.fn();
    const message = "No inputs were found in config file 'tsconfig.json'.";
    check = {
      ...base,
      status: "errors",
      result: { ...result, status: "errors", diagnostics: [{ file: "", line: 0, column: 0, category: "error", code: "TS18003", message }] },
    };
    render(<SiteTypecheckCard site="marketing" release={12} canCheck onOpenFile={onOpenFile} />);
    expect(screen.getByText("TS18003")).toBeInTheDocument();
    expect(screen.getByText(message)).toBeInTheDocument();
    expect(screen.queryByText("0")).toBeNull();
    expect(screen.queryByRole("button", { name: "" })).toBeNull();
  });

  it("says how much of a check went to preparing the compiler, from a second up (#2833)", () => {
    check = { ...base, result: { ...result, status: "clean", duration_ms: 162140, compile_ms: 103727 } };
    const { unmount } = render(<SiteTypecheckCard site="marketing" release={12} canCheck />);
    expect(screen.getByText(/in 162 s \(104 s of it preparing the compiler\)/)).toBeInTheDocument();
    unmount();
    check = { ...base, result: { ...result, status: "clean", duration_ms: 3000, compile_ms: 251 } };
    render(<SiteTypecheckCard site="marketing" release={12} canCheck />);
    expect(screen.queryByText(/preparing the compiler/)).toBeNull();
  });

  it("says how many checks wait ahead of a queued one, with no Check again", () => {
    check = { ...base, status: "queued", ahead: 2 };
    render(<SiteTypecheckCard site="marketing" release={12} canCheck />);
    expect(screen.getByText("Waiting")).toBeInTheDocument();
    expect(screen.getByText(/2 releases are ahead of this one/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Check again/ })).toBeNull();
  });

  it("shows the server's sentence for a check that did not finish", () => {
    check = { ...base, status: "over_limit", result: { ...result, status: "over_limit", message: "The check needed more than the 16 MiB a check may use and was stopped." } };
    render(<SiteTypecheckCard site="marketing" release={12} canCheck={false} />);
    expect(screen.getByText("Not finished")).toBeInTheDocument();
    expect(screen.getByText(/16 MiB a check may use/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Check again/ })).toBeNull();
  });

  it("reads every status as a badge", () => {
    expect(typecheckBadge("queued", 0).label).toBe("Waiting");
    expect(typecheckBadge("running", 0).label).toBe("Checking");
    expect(typecheckBadge("clean", 0).label).toBe("No errors");
    expect(typecheckBadge("errors", 3).label).toBe("3 errors");
    expect(typecheckBadge("unsupported", 0).label).toBe("Cannot check");
    expect(typecheckBadge("failed", 0).label).toBe("Not finished");
    render(<TypecheckBadge />);
    expect(screen.getByText("Not checked")).toBeInTheDocument();
  });
});
