import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook } from "@testing-library/react";

// Hold the branding payload the mocked hook returns, so each test can vary it.
const { branding } = vi.hoisted(() => ({ branding: { value: null as unknown } }));
vi.mock("@/api/portal/hooks", () => ({
  useBranding: () => ({ data: branding.value }),
}));

import { usePortalTitle, DEFAULT_PORTAL_TITLE } from "./usePortalTitle";

beforeEach(() => {
  branding.value = null;
  document.title = "";
});

describe("usePortalTitle", () => {
  it("uses the server-composed title and puts it on the tab", () => {
    branding.value = { portal_title: "ACME Portal" };
    const { result } = renderHook(() => usePortalTitle());
    expect(result.current.portalTitle).toBe("ACME Portal");
    expect(document.title).toBe("ACME Portal");
  });

  // The tab is the one place a stale product name is most visible, so an
  // unreachable branding endpoint must still leave a sensible name there.
  it("falls back to the product name when branding is unavailable", () => {
    const { result } = renderHook(() => usePortalTitle());
    expect(result.current.portalTitle).toBe(DEFAULT_PORTAL_TITLE);
    expect(document.title).toBe(DEFAULT_PORTAL_TITLE);
  });

  it("falls back when the deployment configured an empty title", () => {
    branding.value = { portal_title: "" };
    const { result } = renderHook(() => usePortalTitle());
    expect(result.current.portalTitle).toBe(DEFAULT_PORTAL_TITLE);
  });

  it("reports the brand URL when one is configured", () => {
    branding.value = { portal_title: "ACME Portal", brand_url: "https://acme.example.com" };
    const { result } = renderHook(() => usePortalTitle());
    expect(result.current.brandURL).toBe("https://acme.example.com");
  });

  it("reports no brand URL when the deployment configured none", () => {
    branding.value = { portal_title: "ACME Portal" };
    const { result } = renderHook(() => usePortalTitle());
    expect(result.current.brandURL).toBe("");
  });
});
