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
