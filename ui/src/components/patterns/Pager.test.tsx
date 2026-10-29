import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Pager } from "./Pager";

afterEach(cleanup);

describe("Pager", () => {
  it("shows the range and the page position", () => {
    render(<Pager page={2} perPage={20} total={45} onPage={vi.fn()} />);
    expect(screen.getByText(/Showing 21.40 of 45/)).toBeInTheDocument();
    expect(screen.getByText("Page 2 of 3")).toBeInTheDocument();
  });

  it("marks a total that stopped at a cap as a lower bound (#2272)", () => {
    const onPage = vi.fn();
    render(<Pager page={500} perPage={20} total={10000} atLeast onPage={onPage} />);
    expect(screen.getByText(/of 10,000\+/)).toBeInTheDocument();
    expect(screen.getByText("Page 500 of 500+")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Previous page" }));
    expect(onPage).toHaveBeenCalledWith(499);
  });
});
