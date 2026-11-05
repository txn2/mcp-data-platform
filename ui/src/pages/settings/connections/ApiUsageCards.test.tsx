import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const h = vi.hoisted(() => ({ usedBy: undefined as unknown, warnings: undefined as unknown, op: undefined as unknown }));

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useConnectionUsedBy: () => ({ data: h.usedBy, isLoading: false }),
    useConnectionWarnings: () => ({ data: h.warnings, isLoading: false }),
    useOperationUsedBy: () => ({ data: h.op, isLoading: false, error: null }),
  };
});

import { ConnectionUsedByCard, UpstreamWarningsCard } from "./ApiUsageCards";
import { OperationUsedBy } from "../catalogs/UsedBy";

describe("ConnectionUsedByCard", () => {
  it("lists what uses each operation, or says why there is nothing", () => {
    h.usedBy = { connection: "stripe", operations: [] };
    const { unmount } = render(<ConnectionUsedByCard connectionName="stripe" />);
    expect(screen.getByText(/reads no catalog/)).toBeInTheDocument();
    unmount();

    h.usedBy = {
      connection: "stripe", catalog_id: "stripe-api",
      operations: [{ operation: "PostRefunds", used_by: [
        { kind: "script", id: "s1", name: "nightly", via: "run", connection: "stripe", last_seen_at: "2026-11-04T00:00:00Z" },
        { kind: "endpoint", id: "refund-hook", name: "refund-hook", via: "source" },
      ] }],
    };
    render(<ConnectionUsedByCard connectionName="stripe" />);
    expect(screen.getByText("PostRefunds")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "nightly" })).toHaveAttribute("href", "/portal/automations/s1");
    expect(screen.getByText(/called in a run on stripe/)).toBeInTheDocument();
    expect(screen.getByText("refund-hook")).toBeInTheDocument();
  });

  it("says no operation is used", () => {
    h.usedBy = { connection: "stripe", catalog_id: "stripe-api", operations: [] };
    render(<ConnectionUsedByCard connectionName="stripe" />);
    expect(screen.getByText(/No script, endpoint or prompt uses/)).toBeInTheDocument();
  });
});

describe("UpstreamWarningsCard", () => {
  it("lists each warning with its kind", () => {
    h.warnings = { connection: "stripe", warnings: [] };
    const { unmount } = render(<UpstreamWarningsCard connectionName="stripe" />);
    expect(screen.getByText(/has not marked an operation deprecated/)).toBeInTheDocument();
    unmount();
    h.warnings = { connection: "stripe", warnings: [
      { connection: "stripe", operation: "GetBalanceHistory", kind: "sunset", detail: "Tue, 30 Jun 2026", first_seen_at: "2026-11-01T00:00:00Z", last_seen_at: "2026-11-05T00:00:00Z", seen_count: 3 },
      { connection: "stripe", operation: "GetChargesSearch", kind: "removed", first_seen_at: "2026-11-01T00:00:00Z", last_seen_at: "2026-11-05T00:00:00Z", seen_count: 1 },
    ] };
    render(<UpstreamWarningsCard connectionName="stripe" />);
    expect(screen.getByText("Sunset")).toBeInTheDocument();
    expect(screen.getByText("Removed")).toBeInTheDocument();
    expect(screen.getByText("Tue, 30 Jun 2026")).toBeInTheDocument();
  });
});

describe("OperationUsedBy", () => {
  it("lists the callers or says nothing uses it", () => {
    h.op = { operation: "listPets", used_by: [] };
    const { unmount } = render(<OperationUsedBy catalogID="acme" specName="pets" operationID="listPets" />);
    expect(screen.getByText(/uses this operation/)).toBeInTheDocument();
    unmount();
    h.op = { operation: "listPets", used_by: [{ kind: "prompt", id: "p1", name: "triage", via: "prompt" }] };
    render(<OperationUsedBy catalogID="acme" specName="pets" operationID="listPets" />);
    expect(screen.getByRole("link", { name: "triage" })).toHaveAttribute("href", "/portal/prompts/p1");
  });
});
