import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import * as hooks from "@/api/admin/hooks";
import { ReferencesCard } from "./ReferencesCard";

vi.mock("@/api/admin/hooks", () => ({ useSystemInfo: vi.fn() }));

describe("ReferencesCard", () => {
  it("shows the deployment's scheme", () => {
    vi.mocked(hooks.useSystemInfo).mockReturnValue({ data: { reference_scheme: "acme" }, isLoading: false } as never);
    render(<ReferencesCard />);
    expect(screen.getByTestId("reference-scheme")).toHaveTextContent("acme");
    expect(screen.getByText("acme:asset:<id>")).toBeInTheDocument();
  });

  it("shows mcp when none is set", () => {
    vi.mocked(hooks.useSystemInfo).mockReturnValue({ data: {}, isLoading: false } as never);
    render(<ReferencesCard />);
    expect(screen.getByText("mcp:asset:<id>")).toBeInTheDocument();
  });
});
