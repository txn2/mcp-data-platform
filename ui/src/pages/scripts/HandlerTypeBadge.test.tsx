import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, within } from "@testing-library/react";
import { HandlerTypeBadge, HandlerTypeBadges } from "./HandlerTypeBadge";

// #2435: what starts a script, named by the words the Type filter offers, on
// every surface that names it (the listing rows, the script's Handlers section
// and Admin > Handlers).

afterEach(cleanup);

describe("HandlerTypeBadge", () => {
  it("names each type in the filter's words, each in a variant of its own", () => {
    const { container } = render(
      <>
        <HandlerTypeBadge type="endpoint" />
        <HandlerTypeBadge type="form" />
        <HandlerTypeBadge type="schedule" />
        <HandlerTypeBadge type="webhook" />
      </>,
    );
    const badges = Array.from(container.querySelectorAll("[data-slot=badge]"));
    expect(badges.map((b) => b.textContent)).toEqual(["Endpoint", "Form", "Schedule", "Webhook"]);
    const variants = badges.map((b) => b.getAttribute("data-variant"));
    expect(new Set(variants).size).toBe(4);
  });

  it("passes its class on", () => {
    render(<HandlerTypeBadge type="form" className="shrink-0" />);
    expect(screen.getByText("Form")).toHaveClass("shrink-0");
  });
});

describe("HandlerTypeBadges", () => {
  it("is a script's types in the order given, once each", () => {
    render(<HandlerTypeBadges types={["form", "schedule", "webhook"]} />);
    const group = screen.getByTestId("handler-types");
    expect(within(group).getAllByText(/./).map((b) => b.textContent)).toEqual(["Form", "Schedule", "Webhook"]);
  });

  it("is nothing for a script with no handler", () => {
    const { container } = render(<HandlerTypeBadges types={[]} />);
    expect(container).toBeEmptyDOMElement();
    cleanup();
    const { container: none } = render(<HandlerTypeBadges />);
    expect(none).toBeEmptyDOMElement();
  });

  // A newer server may report a type this portal does not know; it is left
  // out rather than drawn as a raw word beside the ones it does.
  it("leaves out a type it does not know", () => {
    render(<HandlerTypeBadges types={["schedule", "mailbox"]} />);
    const group = screen.getByTestId("handler-types");
    expect(group).toHaveTextContent("Schedule");
    expect(group).not.toHaveTextContent("mailbox");
    cleanup();
    const { container } = render(<HandlerTypeBadges types={["mailbox"]} />);
    expect(container).toBeEmptyDOMElement();
  });
});
