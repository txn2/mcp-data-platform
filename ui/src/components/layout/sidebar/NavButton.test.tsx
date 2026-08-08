import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MessageCircle } from "lucide-react";
import { NavButton } from "./NavButton";

const badge = { count: 3, label: "3 feedback items need you" };

describe("NavButton", () => {
  it("names the section and reports it as the current page when active", () => {
    render(
      <NavButton icon={MessageCircle} label="Feedback" collapsed={false} active onClick={vi.fn()} />,
    );

    const btn = screen.getByRole("button", { name: /feedback/i });
    expect(btn).toHaveAttribute("aria-current", "page");
  });

  it("leaves aria-current off a section the reader is not in", () => {
    render(
      <NavButton icon={MessageCircle} label="Feedback" collapsed={false} onClick={vi.fn()} />,
    );

    expect(screen.getByRole("button", { name: /feedback/i })).not.toHaveAttribute(
      "aria-current",
    );
  });

  it("shows the waiting count on an expanded rail", () => {
    render(
      <NavButton
        icon={MessageCircle}
        label="Feedback"
        collapsed={false}
        badge={badge}
        onClick={vi.fn()}
      />,
    );

    expect(screen.getByText("3")).toBeInTheDocument();
  });

  it("replaces the count with a named dot on a collapsed rail", () => {
    render(
      <NavButton
        icon={MessageCircle}
        label="Feedback"
        collapsed
        badge={badge}
        onClick={vi.fn()}
      />,
    );

    // The figure has no room, but the cue must survive the collapse: the dot
    // carries the words a reader would otherwise lose.
    expect(screen.queryByText("3")).not.toBeInTheDocument();
    expect(screen.getByLabelText("3 feedback items need you")).toBeInTheDocument();
    // The words are gone from the face, so the row is named by its hover copy.
    expect(screen.getByRole("button")).toHaveAttribute("title", "Feedback");
  });

  it("shows nothing at all when nothing is waiting", () => {
    render(
      <NavButton
        icon={MessageCircle}
        label="Feedback"
        collapsed={false}
        badge={{ count: 0, label: "0 feedback items need you" }}
        onClick={vi.fn()}
      />,
    );

    expect(screen.queryByText("0")).not.toBeInTheDocument();
  });

  it("navigates on click", () => {
    const onClick = vi.fn();
    render(
      <NavButton icon={MessageCircle} label="Feedback" collapsed={false} onClick={onClick} />,
    );

    fireEvent.click(screen.getByRole("button", { name: /feedback/i }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
