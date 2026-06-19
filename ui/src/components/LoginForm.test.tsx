import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

// Hold the branding payload the mocked hook returns, so each test can vary it.
const { branding } = vi.hoisted(() => ({ branding: { value: null as unknown } }));
vi.mock("@/api/portal/hooks", () => ({
  useBranding: () => ({ data: branding.value }),
}));

import { LoginForm } from "./LoginForm";

// jsdom has no matchMedia; LoginForm reads it for theme detection.
beforeEach(() => {
  window.matchMedia = vi.fn().mockReturnValue({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }) as unknown as typeof window.matchMedia;
});

describe("LoginForm OIDC button label", () => {
  beforeEach(() => {
    branding.value = null;
  });

  it("shows the default label when no override is configured", () => {
    branding.value = { oidc_enabled: true };
    render(<LoginForm />);
    expect(screen.getByText("Sign in with OIDC")).toBeInTheDocument();
  });

  it("shows the configured override label instead of the default", () => {
    branding.value = { oidc_enabled: true, oidc_button_label: "Sign in with ACME Keycloak" };
    render(<LoginForm />);
    expect(screen.getByText("Sign in with ACME Keycloak")).toBeInTheDocument();
    expect(screen.queryByText("Sign in with OIDC")).not.toBeInTheDocument();
  });
});
