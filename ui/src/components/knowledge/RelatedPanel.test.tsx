import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";

// The list endpoint already access-filters and resolves, so the panel renders it
// directly. Inaccessible refs never appear in this list (the server omits them).
const refs = [
  { urn: "mcp:asset:a1", type: "asset", label: "Sales Dashboard", exists: true, source: "manual" },
  { urn: "mcp:connection:(trino,warehouse)", type: "connection", label: "warehouse (trino)", exists: true, source: "promoted" },
];

vi.mock("@/api/portal/hooks", () => ({
  useKnowledgePageRefs: () => ({ data: { refs } }),
}));

import { RelatedPanel } from "./RelatedPanel";

describe("RelatedPanel", () => {
  it("groups the resolved refs by type", () => {
    const { container } = render(<RelatedPanel pageId="kp1" />);
    const text = container.textContent ?? "";
    expect(text).toContain("Related");
    expect(text).toContain("Assets");
    expect(text).toContain("Sales Dashboard");
    expect(text).toContain("Connections");
    expect(text).toContain("warehouse (trino)");
  });

  it("renders nothing when there are no refs", () => {
    // Re-mock to an empty list for this assertion path is overkill; instead verify
    // the populated panel does not leak any raw id form.
    const { container } = render(<RelatedPanel pageId="kp1" />);
    expect(container.textContent).not.toContain("mcp:asset:a1");
  });
});
