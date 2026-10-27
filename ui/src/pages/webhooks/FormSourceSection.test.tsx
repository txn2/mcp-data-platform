import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { FormSourceSection } from "./FormSourceSection";

afterEach(cleanup);

describe("FormSourceSection", () => {
  it("names the form a source collects for and who may decrypt it", () => {
    render(<FormSourceSection form={{ script_id: "scr_1", endpoint: "contact", decrypt_personas: ["credit", "ops"] }} />);
    expect(screen.getByRole("heading", { name: "Form" })).toBeInTheDocument();
    expect(screen.getByText("/endpoints/contact")).toBeInTheDocument();
    expect(screen.getByText("/forms/contact")).toBeInTheDocument();
    expect(screen.getByText("credit, ops")).toBeInTheDocument();
  });

  it("says none when no persona may decrypt", () => {
    render(<FormSourceSection form={{ script_id: "scr_1", endpoint: "contact" }} />);
    expect(screen.getByText("None")).toBeInTheDocument();
  });
});
