import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";

// Hold the branding payload the mocked hook returns, so each test can vary it.
const { branding } = vi.hoisted(() => ({ branding: { value: null as unknown } }));
vi.mock("@/api/portal/hooks", () => ({
  useBranding: () => ({ data: branding.value }),
}));

import { Header } from "./Header";

beforeEach(() => {
  window.matchMedia = vi.fn().mockReturnValue({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }) as unknown as typeof window.matchMedia;
  branding.value = { version: "1.120.0" };
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("Header version badge", () => {
  it("shows the running version", () => {
    render(<Header title="Assets" />);
    expect(screen.getByText("v1.120.0")).toBeInTheDocument();
  });

  it("links the version when the deployment configured a target", () => {
    branding.value = { version: "1.120.0", version_url: "https://acme.example.com/changelog" };
    render(<Header title="Assets" />);
    const link = screen.getByRole("link", { name: "v1.120.0" });
    expect(link).toHaveAttribute("href", "https://acme.example.com/changelog");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  // No configured target means no link: a reader should not be offered one that
  // goes nowhere.
  it("leaves the version as plain text when no target is configured", () => {
    render(<Header title="Assets" />);
    expect(screen.queryByRole("link", { name: "v1.120.0" })).not.toBeInTheDocument();
  });

  it("omits the version entirely when the server reported none", () => {
    branding.value = {};
    render(<Header title="Assets" />);
    expect(screen.queryByText(/^v/)).not.toBeInTheDocument();
  });
});
