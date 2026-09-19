import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  useKnowledgeBacklinks: (urn: string) => ({
    data:
      urn === "mcp:asset:none"
        ? { pages: [] }
        : {
            pages: [
              { id: "kp1", slug: "fiscal", title: "Fiscal Calendar" },
              { id: "kp2", slug: "slides", title: "Building slides" },
            ],
          },
  }),
}));

import { KnowledgeBacklinksButton } from "./KnowledgeBacklinksButton";

describe("KnowledgeBacklinksButton (#1792)", () => {
  it("carries the count on the button and lists nothing until opened", () => {
    render(<KnowledgeBacklinksButton urn="mcp:asset:a1" onNavigate={vi.fn()} />);
    const button = screen.getByRole("button", { name: /referenced by/i });
    expect(within(button).getByText("2")).toBeInTheDocument();
    expect(button).toHaveAttribute("title", "2 knowledge pages reference this");
    expect(screen.queryByText("Fiscal Calendar")).not.toBeInTheDocument();
  });

  it("opens a modal of the pages, each of which opens its page", () => {
    const onNavigate = vi.fn();
    render(<KnowledgeBacklinksButton urn="mcp:asset:a1" onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: /referenced by/i }));

    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("2 knowledge pages reference this.")).toBeInTheDocument();
    expect(within(dialog).getByText("Building slides")).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: /fiscal calendar/i }));
    expect(onNavigate).toHaveBeenCalledWith("/knowledge/pages/kp1");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("lists the pages without links when there is nowhere to navigate", () => {
    render(<KnowledgeBacklinksButton urn="mcp:asset:a1" />);
    fireEvent.click(screen.getByRole("button", { name: /referenced by/i }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("Fiscal Calendar")).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: /fiscal calendar/i })).not.toBeInTheDocument();
  });

  it("renders nothing when no page references the entity", () => {
    const { container } = render(<KnowledgeBacklinksButton urn="mcp:asset:none" />);
    expect(container.firstChild).toBeNull();
  });
});
