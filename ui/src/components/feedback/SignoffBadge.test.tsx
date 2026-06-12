import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({ useSignoff: vi.fn() }));

import { SignoffBadge } from "./SignoffBadge";
import { useSignoff } from "@/api/portal/hooks";

const mockUseSignoff = vi.mocked(useSignoff);

describe("SignoffBadge", () => {
  it("renders 'signed off by N of M'", () => {
    mockUseSignoff.mockReturnValue({ data: { signed_off: 1, stakeholders: 3 } } as never);
    render(<SignoffBadge targetType="assets" id="a1" />);
    expect(screen.getByText(/signed off by 1 of 3/i)).toBeInTheDocument();
  });

  it("renders nothing until the summary loads", () => {
    mockUseSignoff.mockReturnValue({ data: undefined } as never);
    const { container } = render(<SignoffBadge targetType="collections" id="c1" />);
    expect(container).toBeEmptyDOMElement();
  });
});
