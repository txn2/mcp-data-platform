import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  useKnowledgeBacklinks: (urn: string) => ({
    data: urn === "mcp:asset:none" ? { pages: [] } : { pages: [{ id: "kp1", slug: "fiscal", title: "Fiscal Calendar" }] },
  }),
}));

import { KnowledgeBacklinks } from "./KnowledgeBacklinks";

describe("KnowledgeBacklinks", () => {
  it("opens the specific referencing page via its detail route (#709)", () => {
    const onNavigate = vi.fn();
    const { container } = render(<KnowledgeBacklinks urn="mcp:asset:a1" onNavigate={onNavigate} />);
    const text = container.textContent ?? "";
    expect(text).toContain("1 knowledge page references this");
    expect(text).toContain("Fiscal Calendar");
    // The backlink is a knowledge_page EntityChip: clicking it routes to that
    // page (p.id), not a generic /knowledge#knowledge anchor.
    fireEvent.click(container.querySelector("a")!);
    expect(onNavigate).toHaveBeenCalledWith("/knowledge/pages/kp1");
  });

  it("renders nothing when there are no referencing pages", () => {
    const { container } = render(<KnowledgeBacklinks urn="mcp:asset:none" />);
    expect(container.firstChild).toBeNull();
  });
});
