import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

let feedback = true;
vi.mock("@/hooks/usePortalAccess", () => ({ usePortalAccess: () => ({ feedback }) }));
vi.mock("@/api/portal/hooks", () => ({
  useThreads: () => ({ data: { data: [{ status: "open" }, { status: "resolved" }] } }),
}));
vi.mock("./FeedbackPanel", () => ({
  FeedbackPanel: ({ onClose }: { onClose: () => void }) => (
    <div>
      <textarea aria-label="Message" />
      <button type="button" onClick={onClose}>
        Close feedback
      </button>
    </div>
  ),
}));

import { PageArea } from "@/components/layout/PageArea";
import { FeedbackButton } from "./FeedbackButton";

const target = { type: "asset", id: "a1" } as const;

function page() {
  render(
    <PageArea>
      <FeedbackButton target={target} />
      <p>the item</p>
    </PageArea>,
  );
}

const open = () => fireEvent.click(screen.getByRole("button", { name: /Feedback/ }));
const slot = () => document.querySelector("[data-item-panel]") as HTMLElement;

beforeEach(() => {
  feedback = true;
});

describe("FeedbackButton", () => {
  it("offers nothing to a persona without Feedback", () => {
    feedback = false;
    page();
    expect(screen.queryByRole("button", { name: /Feedback/ })).toBeNull();
  });

  it("counts the open threads", () => {
    page();
    expect(screen.getByRole("button", { name: /Feedback/ }).textContent).toContain("1");
  });

  it("puts the panel in the space beside the page, leaving the page uncovered", () => {
    page();
    expect(slot().childElementCount).toBe(0);
    open();
    expect(slot().contains(screen.getByLabelText("Message"))).toBe(true);
    expect(screen.getByRole("main").contains(screen.getByLabelText("Message"))).toBe(false);
    expect(document.querySelector(".fixed.inset-0")).toBeNull();
  });

  it("keeps the panel and what was typed when the page is clicked", () => {
    page();
    open();
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "slide 3 is wrong" } });
    fireEvent.click(screen.getByText("the item"));
    expect((screen.getByLabelText("Message") as HTMLTextAreaElement).value).toBe("slide 3 is wrong");
  });

  it("empties the space when closed", () => {
    page();
    open();
    fireEvent.click(screen.getByRole("button", { name: "Close feedback" }));
    expect(slot().childElementCount).toBe(0);
  });

  it("stays in place with no space beside the page", () => {
    render(<FeedbackButton target={target} />);
    open();
    expect(screen.getByLabelText("Message")).toBeTruthy();
  });

  describe("on a narrow screen", () => {
    const width = window.innerWidth;
    const content = (bottom: number) => {
      const row = document.createElement("div");
      row.innerHTML = '<div data-testid="content-controls"></div>';
      row.getBoundingClientRect = () => ({ bottom }) as DOMRect;
      screen.getByRole("main").appendChild(row);
    };

    afterEach(() => {
      window.innerWidth = width;
    });

    it("scrolls the page's title and toolbars away so the item shows above the panel", () => {
      window.innerWidth = 390;
      page();
      content(250);
      const main = screen.getByRole("main");
      main.getBoundingClientRect = () => ({ top: 56 }) as DOMRect;
      open();
      expect(main.scrollTop).toBe(194);
    });

    it("leaves a reader who has scrolled past them where they are", () => {
      window.innerWidth = 390;
      page();
      content(20);
      const main = screen.getByRole("main");
      main.getBoundingClientRect = () => ({ top: 56 }) as DOMRect;
      main.scrollTop = 900;
      open();
      expect(main.scrollTop).toBe(900);
    });

    it("does not move the page with the panel beside it", () => {
      window.innerWidth = 1280;
      page();
      content(250);
      open();
      expect(screen.getByRole("main").scrollTop).toBe(0);
    });
  });
});
