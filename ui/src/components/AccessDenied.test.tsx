import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

// Hold the branding payload the mocked hook returns, so each test can vary it.
const { branding } = vi.hoisted(() => ({ branding: { value: null as unknown } }));
vi.mock("@/api/portal/hooks", () => ({
  useBranding: () => ({ data: branding.value }),
}));

import { AccessDenied } from "./AccessDenied";
import { useAuthStore } from "@/stores/auth";

// jsdom has no matchMedia; the component reads it for theme detection.
beforeEach(() => {
  window.matchMedia = vi.fn().mockReturnValue({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }) as unknown as typeof window.matchMedia;
  branding.value = null;
});

afterEach(() => {
  useAuthStore.setState({ accessDenied: false, deniedEmail: "", authMethod: null });
  vi.restoreAllMocks();
});

describe("AccessDenied", () => {
  it("explains that the account maps to no persona", () => {
    render(<AccessDenied />);
    expect(screen.getByText(/not assigned to a persona/i)).toBeInTheDocument();
  });

  // Naming the refused account is the whole point: it is what the person has to
  // tell an administrator to get granted.
  it("names the account that was refused", () => {
    useAuthStore.setState({ deniedEmail: "nobody@example.com" });
    render(<AccessDenied />);
    expect(screen.getByText(/nobody@example.com/)).toBeInTheDocument();
    expect(screen.getByText(/Signed in as/i)).toBeInTheDocument();
  });

  it("omits the account row when the server named no address", () => {
    useAuthStore.setState({ deniedEmail: "" });
    render(<AccessDenied />);
    expect(screen.queryByText(/Signed in as/i)).not.toBeInTheDocument();
  });

  // Offering sign-in would loop the caller through an identity provider that
  // already accepted them; the way out is a different account.
  it("offers sign out rather than sign in", () => {
    render(<AccessDenied />);
    expect(screen.getByRole("button", { name: /sign out/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^sign in/i })).not.toBeInTheDocument();
  });

  it("signs out through the store when the button is clicked", () => {
    const logout = vi.fn();
    useAuthStore.setState({ logout });
    render(<AccessDenied />);

    fireEvent.click(screen.getByRole("button", { name: /sign out/i }));

    expect(logout).toHaveBeenCalledTimes(1);
  });

  it("renders the deployment's portal title when branding configures one", () => {
    branding.value = { portal_title: "ACME Data" };
    render(<AccessDenied />);
    expect(screen.getByText("ACME Data")).toBeInTheDocument();
  });

  it("falls back to the default title with no branding", () => {
    render(<AccessDenied />);
    expect(screen.getByText("MCP Data Platform")).toBeInTheDocument();
  });
});
