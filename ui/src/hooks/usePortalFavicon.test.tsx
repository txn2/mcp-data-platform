import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook } from "@testing-library/react";

const { branding } = vi.hoisted(() => ({ branding: { value: null as unknown } }));
vi.mock("@/api/portal/hooks", () => ({
  useBranding: () => ({ data: branding.value }),
}));

import { usePortalFavicon } from "./usePortalFavicon";

function iconLink(): HTMLLinkElement | null {
  return document.querySelector<HTMLLinkElement>("link[rel='icon']");
}

beforeEach(() => {
  window.matchMedia = vi.fn().mockReturnValue({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }) as unknown as typeof window.matchMedia;
  branding.value = null;
  document.head.innerHTML = "";
});

describe("usePortalFavicon", () => {
  it("points the shell's icon link at the deployment's logo and drops its type", () => {
    document.head.innerHTML = '<link rel="icon" type="image/svg+xml" href="/favicon.ico">';
    branding.value = { portal_logo: "https://acme.example.com/mark.png" };
    renderHook(() => usePortalFavicon());
    const link = iconLink();
    expect(link?.getAttribute("href")).toBe("https://acme.example.com/mark.png");
    // A PNG logo under an svg type is skipped by the browser.
    expect(link?.hasAttribute("type")).toBe(false);
  });

  it("adds an icon link with the bundled mark when the page has none and no logo is configured", () => {
    renderHook(() => usePortalFavicon());
    expect(iconLink()?.getAttribute("href")).toMatch(/images\/activity-svgrepo-com\.svg$/);
  });
});
