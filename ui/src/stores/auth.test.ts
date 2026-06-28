import { describe, it, expect, afterEach, vi } from "vitest";
import { useAuthStore } from "./auth";

describe("loginOIDC return_to capture (#710)", () => {
  const originalLocation = window.location;

  // jsdom's window.location is mostly read-only; replace it with a
  // controllable stand-in so we can capture the assigned href without an
  // actual navigation tearing down the test.
  function mockLocation(pathname: string, search = "", hash = ""): { get href(): string } {
    let href = "";
    Object.defineProperty(window, "location", {
      configurable: true,
      value: {
        pathname,
        search,
        hash,
        set href(v: string) {
          href = v;
        },
      },
    });
    return {
      get href() {
        return href;
      },
    };
  }

  afterEach(() => {
    Object.defineProperty(window, "location", {
      configurable: true,
      value: originalLocation,
    });
    vi.restoreAllMocks();
  });

  it("redirects to the login endpoint carrying the current path as return_to", () => {
    const loc = mockLocation("/assets/asset-001");
    useAuthStore.getState().loginOIDC();
    expect(loc.href).toBe(
      "/portal/auth/login?return_to=" + encodeURIComponent("/assets/asset-001"),
    );
  });

  it("includes query and hash in the captured return_to", () => {
    const loc = mockLocation("/knowledge", "?tab=pages", "#section-2");
    useAuthStore.getState().loginOIDC();
    expect(loc.href).toBe(
      "/portal/auth/login?return_to=" +
        encodeURIComponent("/knowledge?tab=pages#section-2"),
    );
  });
});
