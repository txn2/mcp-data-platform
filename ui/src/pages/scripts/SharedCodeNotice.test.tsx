import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { SharedCodeNotice } from "./SharedCodeNotice";

afterEach(cleanup);

// A save that went through says which of its functions already exist (#2273):
// a library's, with the load line that replaces the copy, or other scripts', by
// name, with how to share them; never their code.
describe("SharedCodeNotice", () => {
  it("names a library match with its load line, and a match in other scripts by name", () => {
    render(
      <SharedCodeNotice
        shared={[
          { function: "prior_week", library: "date-windows", version: 1, library_function: "last_week", load: 'load("lib:date-windows@1", "last_week")', next: "" },
          { function: "page_through", scripts: ["nightly-sync", "export-orders"], next: "" },
        ]}
      />,
    );
    expect(screen.getByText("2 functions in this script already exist elsewhere")).toBeInTheDocument();
    const [lib, others] = screen.getAllByTestId("shared-code-match");
    expect(lib?.textContent).toContain("prior_week is last_week in the library date-windows, version 1.");
    expect(lib?.textContent).toContain('load("lib:date-windows@1", "last_week")');
    expect(others?.textContent).toContain("page_through is also in nightly-sync, export-orders.");
    expect(others?.textContent).toContain("only with their owners' agreement");
    expect(screen.getByText(/it never shows their code/)).toBeInTheDocument();
  });

  it("says one function in the singular, and nothing when nothing is shared", () => {
    const { rerender } = render(<SharedCodeNotice shared={[{ function: "page_through", scripts: ["a"], next: "" }]} />);
    expect(screen.getByText("One function in this script already exists elsewhere")).toBeInTheDocument();
    rerender(<SharedCodeNotice shared={[]} />);
    expect(screen.queryByTestId("shared-code-notice")).not.toBeInTheDocument();
  });
});
