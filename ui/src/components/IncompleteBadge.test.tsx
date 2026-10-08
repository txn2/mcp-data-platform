import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { IncompleteBadge, IncompleteNotice } from "./IncompleteBadge";

afterEach(cleanup);

describe("IncompleteBadge", () => {
  it("reads Incomplete and carries the detail as its title", () => {
    render(<IncompleteBadge title="Incomplete: truncated at 10 rows" />);
    expect(screen.getByText("Incomplete")).toHaveAttribute("title", "Incomplete: truncated at 10 rows");
  });

  it("has a title without one given", () => {
    render(<IncompleteBadge />);
    expect(screen.getByText("Incomplete")).toHaveAttribute("title", "Incomplete: an export limit cut this file");
  });

  it("states the limit and what it means in the notice", () => {
    render(<IncompleteNotice label="Incomplete: truncated at 100,000 rows" />);
    const notice = screen.getByTestId("incomplete-notice");
    expect(notice).toHaveTextContent("Incomplete: truncated at 100,000 rows");
    expect(notice).toHaveTextContent("the file holds only part of it.");
  });
});
