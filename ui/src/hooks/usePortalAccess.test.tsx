import { describe, it, expect, afterEach } from "vitest";
import { render, renderHook, screen } from "@testing-library/react";
import { useAuthStore } from "@/stores/auth";
import { FULL_PORTAL } from "@/lib/portalAccess";
import { SectionHiddenNotice } from "@/components/layout/RouteFallbacks";
import { SectionGate } from "@/components/layout/SectionGate";
import { usePortalAccess } from "./usePortalAccess";

// The caller's portal permissions as the pages read them (#2225).

afterEach(() => useAuthStore.setState({ user: null }));

function signIn(portal?: Record<string, unknown>) {
  useAuthStore.setState({
    user: {
      user_id: "u1",
      email: "a@example.com",
      roles: [],
      is_admin: false,
      portal,
    } as never,
  });
}

describe("usePortalAccess", () => {
  it("is every area at its highest level when /me sent no block, and the later settings unset", () => {
    signIn(undefined);
    const { result } = renderHook(() => usePortalAccess());
    expect(result.current).toEqual({ ...FULL_PORTAL, history: false, external_files: "none" });
  });

  it("takes each area /me sent and fills the rest", () => {
    signIn({ assets: "shared", automations: false, external: true });
    const { result } = renderHook(() => usePortalAccess());
    expect(result.current.assets).toBe("shared");
    expect(result.current.automations).toBe(false);
    expect(result.current.external).toBe(true);
    expect(result.current.prompts).toBe(true);
  });

  it("returns the same object across renders while the block is unchanged", () => {
    signIn({ assets: "shared" });
    const { result, rerender } = renderHook(() => usePortalAccess());
    const first = result.current;
    rerender();
    expect(result.current).toBe(first);
  });
});

describe("SectionHiddenNotice", () => {
  it("names the section the persona does not include", () => {
    render(<SectionHiddenNotice section="Automations" />);
    expect(screen.getByText("Access denied")).toBeInTheDocument();
    expect(
      screen.getByText(/Automations is not available to your persona/),
    ).toBeInTheDocument();
  });
});

describe("SectionGate", () => {
  it("renders the pages of a section the persona includes", () => {
    signIn({ automations: true });
    render(
      <SectionGate route="/automations/s1" adminRoute={false}>
        <p>the pages</p>
      </SectionGate>,
    );
    expect(screen.getByText("the pages")).toBeInTheDocument();
  });

  it("renders the notice, named by the navigation label, for one it does not", () => {
    signIn({ automations: false });
    render(
      <SectionGate route="/automations/s1" adminRoute={false}>
        <p>the pages</p>
      </SectionGate>,
    );
    expect(screen.queryByText("the pages")).not.toBeInTheDocument();
    expect(screen.getByText(/Automations is not available to your persona/)).toBeInTheDocument();
  });

  it("leaves an administrator route and a route no area governs alone", () => {
    signIn({ automations: false, prompts: false });
    render(
      <>
        <SectionGate route="/automations" adminRoute>
          <p>admin pages</p>
        </SectionGate>
        <SectionGate route="/" adminRoute={false}>
          <p>assets</p>
        </SectionGate>
      </>,
    );
    expect(screen.getByText("admin pages")).toBeInTheDocument();
    expect(screen.getByText("assets")).toBeInTheDocument();
  });
});
