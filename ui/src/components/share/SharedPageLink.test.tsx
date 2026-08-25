import { describe, it, expect, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { SharedPageLink } from "./SharedPageLink";

const TOKEN = "b".repeat(64);

describe("SharedPageLink (#1473)", () => {
  const originalLocation = window.location;

  function mockSearch(search: string): void {
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { ...originalLocation, search },
    });
  }

  afterEach(() => {
    Object.defineProperty(window, "location", {
      configurable: true,
      value: originalLocation,
    });
  });

  it("offers the shared page to a reader who arrived on a share link", () => {
    mockSearch(`?share=${TOKEN}`);
    render(<SharedPageLink />);
    const link = screen.getByRole("link", { name: /shared page/i });
    expect(link.getAttribute("href")).toBe(`/portal/view/${TOKEN}?public=1`);
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
  });

  it("renders nothing on a page reached any other way", () => {
    mockSearch("");
    const { container } = render(<SharedPageLink />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing for a token that is not the shape the server issues", () => {
    mockSearch("?share=//evil.example.com");
    const { container } = render(<SharedPageLink />);
    expect(container).toBeEmptyDOMElement();
  });
});
