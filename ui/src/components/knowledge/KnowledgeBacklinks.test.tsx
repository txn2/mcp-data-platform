import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  useKnowledgeBacklinks: (urn: string) => ({
    data: urn === "mcp:asset:none" ? { pages: [] } : { pages: [{ id: "kp1", slug: "fiscal", title: "Fiscal Calendar" }] },
  }),
}));

import { KnowledgeBacklinks } from "./KnowledgeBacklinks";

describe("KnowledgeBacklinks", () => {
  it("surfaces the referencing pages with a count", () => {
    const onNavigate = vi.fn();
    const { container } = render(<KnowledgeBacklinks urn="mcp:asset:a1" onNavigate={onNavigate} />);
    const text = container.textContent ?? "";
    expect(text).toContain("1 knowledge page references this");
    expect(text).toContain("Fiscal Calendar");
    fireEvent.click(container.querySelector("button")!);
    expect(onNavigate).toHaveBeenCalledWith("/knowledge#knowledge");
  });

  it("renders nothing when there are no referencing pages", () => {
    const { container } = render(<KnowledgeBacklinks urn="mcp:asset:none" />);
    expect(container.firstChild).toBeNull();
  });
});
