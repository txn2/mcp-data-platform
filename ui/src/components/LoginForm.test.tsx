import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

// Hold the branding payload the mocked hook returns, so each test can vary it.
const { branding } = vi.hoisted(() => ({
  branding: { value: null as unknown, loading: false },
}));
vi.mock("@/api/portal/hooks", () => ({
  useBranding: () => ({ data: branding.value, isLoading: branding.loading }),
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

// #2242: portal.login_redirect sends a signed-out visitor straight to the
// identity provider, except after the provider refused the sign-in and where
// ?login=api_key asks for the key form.
describe("LoginForm login redirect", () => {
  const originalLocation = window.location;
  let replace: ReturnType<typeof vi.fn>;

  // jsdom's window.location is mostly read-only; replace it with a stand-in
  // that records the navigation instead of performing it.
  function visit(pathname: string, search = "") {
    replace = vi.fn();
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { pathname, search, hash: "", replace, href: pathname + search },
    });
  }

  beforeEach(() => {
    branding.loading = false;
    // As a browser does, replacing the URL changes what location reports.
    vi.spyOn(window.history, "replaceState").mockImplementation((_s, _t, url) => {
      const next = new URL(String(url), "http://portal.test");
      Object.assign(window.location, { pathname: next.pathname, search: next.search });
    });
  });

  afterEach(() => {
    Object.defineProperty(window, "location", { configurable: true, value: originalLocation });
    vi.restoreAllMocks();
  });

  it("goes to the identity provider with the page opened as return_to, drawing no login screen", () => {
    branding.value = { oidc_enabled: true, login_redirect: true, allow_api_key_login: true };
    visit("/portal/assets/a1");
    render(<LoginForm />);
    expect(replace).toHaveBeenCalledWith(
      "/portal/auth/login?return_to=" + encodeURIComponent("/portal/assets/a1"),
    );
    expect(screen.queryByPlaceholderText("X-API-Key")).not.toBeInTheDocument();
    expect(screen.queryByText("Sign in with OIDC")).not.toBeInTheDocument();
  });

  it("shows the provider's refusal and a try-again button, and redirects only when it is pressed", () => {
    branding.value = { oidc_enabled: true, login_redirect: true, allow_api_key_login: true };
    visit("/portal/", "?error=access_denied");
    render(<LoginForm />);
    expect(replace).not.toHaveBeenCalled();
    expect(screen.getByText("Access was denied by the identity provider.")).toBeInTheDocument();
    expect(screen.queryByPlaceholderText("X-API-Key")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(window.location.href).toBe("/portal/auth/login?return_to=" + encodeURIComponent("/portal/"));
    expect(replace).not.toHaveBeenCalled();
  });

  it("offers the API key form at ?login=api_key", () => {
    branding.value = { oidc_enabled: true, login_redirect: true, allow_api_key_login: true };
    visit("/portal/", "?login=api_key");
    render(<LoginForm />);
    expect(replace).not.toHaveBeenCalled();
    expect(screen.getByPlaceholderText("X-API-Key")).toBeInTheDocument();
    expect(screen.getByText("Sign in with OIDC")).toBeInTheDocument();
  });

  it("ignores ?login=api_key when the key form is turned off", () => {
    branding.value = { oidc_enabled: true, login_redirect: true, allow_api_key_login: false };
    visit("/portal/", "?login=api_key");
    render(<LoginForm />);
    expect(replace).toHaveBeenCalledTimes(1);
    expect(screen.queryByPlaceholderText("X-API-Key")).not.toBeInTheDocument();
  });

  it("leaves the login screen unchanged with the setting off", () => {
    branding.value = { oidc_enabled: true, login_redirect: false, allow_api_key_login: true };
    visit("/portal/assets/a1");
    render(<LoginForm />);
    expect(replace).not.toHaveBeenCalled();
    expect(screen.getByText("Sign in with OIDC")).toBeInTheDocument();
    expect(screen.getByText("or use an API key")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("X-API-Key")).toBeInTheDocument();
  });

  it("draws nothing to sign in with until branding has answered", () => {
    branding.value = undefined;
    branding.loading = true;
    visit("/portal/");
    render(<LoginForm />);
    expect(replace).not.toHaveBeenCalled();
    expect(screen.queryByPlaceholderText("X-API-Key")).not.toBeInTheDocument();
  });
});
