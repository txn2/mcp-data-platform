import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

import { PostgresConfigForm } from "./PostgresConfigForm";

describe("PostgresConfigForm", () => {
  it("writes the address fields under the toolkit's keys", () => {
    const onChange = vi.fn();
    render(<PostgresConfigForm config={{}} onChange={onChange} />);

    fireEvent.change(screen.getByLabelText(/^host/i), { target: { value: "db.example.com" } });
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ host: "db.example.com" }));

    fireEvent.change(screen.getByLabelText(/^port/i), { target: { value: "6432" } });
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ port: 6432 }));

    fireEvent.change(screen.getByLabelText(/^database/i), { target: { value: "orders" } });
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ database: "orders" }));
  });

  it("shows the TLS material unless sslmode is disable", () => {
    const { rerender } = render(<PostgresConfigForm config={{}} onChange={vi.fn()} />);
    expect(screen.getByLabelText(/ca bundle/i)).toBeInTheDocument();

    rerender(<PostgresConfigForm config={{ sslmode: "disable" }} onChange={vi.fn()} />);
    expect(screen.queryByLabelText(/ca bundle/i)).not.toBeInTheDocument();
  });

  it("writes read_only when toggled", () => {
    const onChange = vi.fn();
    render(<PostgresConfigForm config={{ host: "db" }} onChange={onChange} />);

    fireEvent.click(screen.getByRole("switch", { name: /read only/i }));

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ host: "db", read_only: true }));
  });
});
