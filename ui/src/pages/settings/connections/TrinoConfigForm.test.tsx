import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

import { TrinoConfigForm } from "./TrinoConfigForm";

// Read Only is the per-connection write gate: the toolkit refuses write SQL on
// a Trino connection whose config carries read_only, so a connection created
// here has no way to be protected unless the form writes the key.
describe("TrinoConfigForm — read_only", () => {
  it("renders the stored value", () => {
    render(<TrinoConfigForm config={{ read_only: true }} onChange={vi.fn()} />);
    expect(screen.getByRole("switch", { name: /read only/i })).toHaveAttribute(
      "aria-checked",
      "true",
    );
  });

  it("writes read_only into the config when toggled on", () => {
    const onChange = vi.fn();
    render(<TrinoConfigForm config={{ host: "trino.example.com" }} onChange={onChange} />);

    fireEvent.click(screen.getByRole("switch", { name: /read only/i }));

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ host: "trino.example.com", read_only: true }),
    );
  });

  it("writes read_only false when toggled off, rather than dropping the key", () => {
    const onChange = vi.fn();
    render(<TrinoConfigForm config={{ read_only: true }} onChange={onChange} />);

    fireEvent.click(screen.getByRole("switch", { name: /read only/i }));

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ read_only: false }));
  });
});
