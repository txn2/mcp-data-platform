import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

// Hold the branding payload the mocked hook returns, so each test can vary it.
const { branding } = vi.hoisted(() => ({ branding: { value: null as unknown } }));
vi.mock("@/api/portal/hooks", () => ({
  useBranding: () => ({ data: branding.value }),
}));
vi.mock("./sidebar/useNavBadges", () => ({
  useNavBadges: () => ({}),
}));

import { Sidebar } from "./Sidebar";
import { useAuthStore } from "@/stores/auth";

function renderSidebar(collapsed = false) {
  return render(
    <Sidebar
      currentPath="/assets"
      onNavigate={vi.fn()}
      collapsed={collapsed}
      onToggleCollapse={vi.fn()}
    />,
  );
}

beforeEach(() => {
  window.matchMedia = vi.fn().mockReturnValue({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }) as unknown as typeof window.matchMedia;
  branding.value = { portal_title: "ACME Portal" };
});

afterEach(() => {
  useAuthStore.setState({ user: null });
  vi.restoreAllMocks();
});

describe("Sidebar brand mark", () => {
  it("shows the deployment title the server composed", () => {
    renderSidebar();
    expect(screen.getByText("ACME Portal")).toBeInTheDocument();
  });

  it("links the mark to the brand site when a brand URL is configured", () => {
    branding.value = { portal_title: "ACME Portal", brand_url: "https://acme.example.com" };
    renderSidebar();
    const link = screen.getByRole("link", { name: "ACME Portal" });
    expect(link).toHaveAttribute("href", "https://acme.example.com");
    // Outward link: it leaves the product, so it must not take the portal tab
    // with it, and must not hand the brand site a window opener.
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  // An unbranded deployment must not get a link to nowhere.
  it("renders the mark as inert markup when no brand URL is configured", () => {
    renderSidebar();
    expect(screen.queryByRole("link", { name: "ACME Portal" })).not.toBeInTheDocument();
  });

  // Collapsed, the logo is all that is left, so the link has to carry the name
  // itself or it announces as bare "link".
  it("names the collapsed mark for assistive technology", () => {
    branding.value = { portal_title: "ACME Portal", brand_url: "https://acme.example.com" };
    renderSidebar(true);
    expect(screen.queryByText("ACME Portal")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "ACME Portal" })).toHaveAttribute(
      "href",
      "https://acme.example.com",
    );
  });

  // A broken logo URL must not leave the collapsed mark an invisible but
  // focusable link, so the image recovers to the bundled mark instead of hiding.
  it("recovers a failed logo to the bundled mark instead of hiding it", () => {
    branding.value = {
      portal_title: "ACME Portal",
      portal_logo: "https://acme.example.com/missing.svg",
      brand_url: "https://acme.example.com",
    };
    const { container } = renderSidebar(true);
    const img = container.querySelector("img")!;
    expect(img).toHaveAttribute("src", "https://acme.example.com/missing.svg");

    fireEvent.error(img);
    expect(img.src).toContain("activity-svgrepo-com");
    expect(img.style.display).not.toBe("none");
  });

  it("adds no tooltip when the name is already visible", () => {
    branding.value = { portal_title: "ACME Portal", brand_url: "https://acme.example.com" };
    renderSidebar();
    expect(screen.getByRole("link", { name: "ACME Portal" })).not.toHaveAttribute("title");
  });
});

describe("Sidebar portal sections", () => {
  it("sets Assets and Resources apart above the other sections, in that order", () => {
    renderSidebar();
    const nav = screen.getByRole("navigation");
    const names = Array.from(nav.querySelectorAll("button")).map((b) => b.textContent?.trim());
    expect(names.slice(0, 2)).toEqual(["Assets", "Resources"]);
    const brk = screen.getByTestId("portal-nav-group-break");
    const resources = screen.getByRole("button", { name: /Resources/ });
    const activity = screen.getByRole("button", { name: /Activity/ });
    // The rule sits between the pinned group and the rest.
    expect(resources.compareDocumentPosition(brk) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(brk.compareDocumentPosition(activity) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getAllByText("User")).toHaveLength(1);
  });
});

describe("Sidebar Automations (#2109)", () => {
  const user = { user_id: "u1", roles: ["viewer"], is_admin: false };

  it("lists Automations for a persona that shows them", () => {
    useAuthStore.setState({ user: { ...user, automations: true } });
    renderSidebar();
    expect(screen.getByRole("button", { name: /Automations/ })).toBeInTheDocument();
  });

  it("leaves Automations out for a persona that hides them", () => {
    useAuthStore.setState({ user: { ...user, automations: false } });
    renderSidebar();
    expect(screen.queryByRole("button", { name: /Automations/ })).not.toBeInTheDocument();
  });

  it("lists Automations when the server does not report the setting", () => {
    useAuthStore.setState({ user });
    renderSidebar();
    expect(screen.getByRole("button", { name: /Automations/ })).toBeInTheDocument();
  });
});

describe("Sidebar signed-in identity (#2215)", () => {
  it("shows the account above Sign Out and opens the profile", () => {
    useAuthStore.setState({ user: { user_id: "u1", email: "ada.lovelace@example.com", roles: [], is_admin: false } });
    const onNavigate = vi.fn();
    render(<Sidebar currentPath="/assets" onNavigate={onNavigate} collapsed={false} onToggleCollapse={vi.fn()} />);
    const entry = screen.getByRole("button", { name: "Profile: ada.lovelace@example.com" });
    expect(entry).toHaveTextContent("AL");
    expect(entry).toHaveTextContent("ada.lovelace@example.com");
    fireEvent.click(entry);
    expect(onNavigate).toHaveBeenCalledWith("/profile");
  });

  it("collapsed, shows the avatar with the account as its hover copy, and an id when there is no email", () => {
    useAuthStore.setState({ user: { user_id: "apikey:ci", roles: [], is_admin: false } });
    renderSidebar(true);
    const entry = screen.getByRole("button", { name: "Profile: apikey:ci" });
    expect(entry).toHaveAttribute("title", "apikey:ci");
    expect(entry).not.toHaveTextContent("apikey:ci");
  });
});
