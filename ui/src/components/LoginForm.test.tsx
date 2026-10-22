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

  it("opens the emailed-link form at ?login=email, never redirected, and says a link is on its way (#2092)", async () => {
    branding.value = { oidc_enabled: true, login_redirect: true, allow_api_key_login: true, email_link_login: true };
    visit("/portal/", "?login=email&error=link_invalid");
    const fetchMock = vi.fn().mockResolvedValue({ ok: true });
    vi.stubGlobal("fetch", fetchMock);
    render(<LoginForm />);
    expect(replace).not.toHaveBeenCalled();
    expect(screen.getByText("That sign-in link has expired or was already used. Ask for a new one.")).toBeInTheDocument();
    fireEvent.change(screen.getByRole("textbox", { name: "Email" }), { target: { value: "client@example.org" } });
    fireEvent.click(screen.getByRole("button", { name: "Email me a sign-in link" }));
    expect(await screen.findByText("If that address has an account, a sign-in link is on its way.")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith("/portal/auth/email-link", expect.objectContaining({ method: "POST" }));
    vi.unstubAllGlobals();
  });

  it("offers Email me a sign-in link where the deployment has external accounts", () => {
    branding.value = { oidc_enabled: true, login_redirect: false, allow_api_key_login: true, email_link_login: true };
    visit("/portal/");
    render(<LoginForm />);
    fireEvent.click(screen.getByRole("button", { name: "Email me a sign-in link" }));
    expect(screen.getByRole("textbox", { name: "Email" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByPlaceholderText("X-API-Key")).toBeInTheDocument();
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

// #2092: an external user asks for an emailed sign-in link; ?login=email opens
// the form, and a used or expired link comes back with link_invalid.
describe("LoginForm email link", () => {
  const originalLocation = window.location;

  function visit(search: string) {
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { pathname: "/portal/", search, hash: "", replace: vi.fn(), href: "/portal/" + search },
    });
  }

  beforeEach(() => {
    branding.loading = false;
    branding.value = { oidc_enabled: true, allow_api_key_login: true, email_link_login: true };
    vi.spyOn(window.history, "replaceState").mockImplementation(() => {});
  });

  afterEach(() => {
    Object.defineProperty(window, "location", { configurable: true, value: originalLocation });
    vi.restoreAllMocks();
  });

  it("offers the link only where the deployment has external accounts", () => {
    visit("");
    branding.value = { oidc_enabled: true, allow_api_key_login: true };
    render(<LoginForm />);
    expect(screen.queryByRole("button", { name: "Email me a sign-in link" })).not.toBeInTheDocument();
  });

  it("asks for a link and says the same thing whatever the address", async () => {
    visit("");
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(null, { status: 202 }));
    render(<LoginForm />);
    fireEvent.click(screen.getByRole("button", { name: "Email me a sign-in link" }));
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: " client@example.org " } });
    fireEvent.click(screen.getByRole("button", { name: "Email me a sign-in link" }));
    expect(await screen.findByText(/If that address has an account/)).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/portal/auth/email-link",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ email: "client@example.org" }) }),
    );
  });

  it("says so when the request fails, and goes back to the other ways in", async () => {
    visit("?login=email");
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(null, { status: 500 }));
    render(<LoginForm />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "client@example.org" } });
    fireEvent.keyDown(screen.getByLabelText("Email"), { key: "Enter" });
    expect(await screen.findByText(/could not be requested/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByPlaceholderText("X-API-Key")).toBeInTheDocument();
  });

  it("explains a link that expired or was used", () => {
    visit("?login=email&error=link_invalid");
    render(<LoginForm />);
    expect(screen.getByText(/That sign-in link has expired or was already used/)).toBeInTheDocument();
    expect(screen.getByLabelText("Email")).toBeInTheDocument();
  });
});
