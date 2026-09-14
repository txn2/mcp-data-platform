import { describe, it, expect, afterEach, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

import { adminNavItems, portalNavItems } from "@/components/layout/sidebar/navItems";
import { isKnownRoute } from "@/lib/portalRoutes";
import { useThemeStore } from "@/stores/theme";
import { ApiReferencePage } from "./ApiReferencePage";
import { redocOptions } from "./redocTheme";

// ReDoc stands in for itself here: what this page decides is which document it
// is pointed at and which palette it is handed, and both travel as props. The
// real component would pull a 1 MB bundle into every run of this file to prove
// the same two things.
vi.mock("redoc", () => ({
  RedocStandalone: ({
    specUrl,
    options,
  }: {
    specUrl?: string;
    options?: { theme?: { colors?: { text?: { primary?: string } } } };
  }) => (
    <div
      data-testid="redoc"
      data-spec-url={specUrl}
      data-text-color={options?.theme?.colors?.text?.primary ?? ""}
    />
  ),
}));

afterEach(() => {
  cleanup();
  useThemeStore.getState().setTheme("light");
});

function textColor(): string {
  return screen.getByTestId("redoc").getAttribute("data-text-color") ?? "";
}

describe("the API reference page", () => {
  it("renders the document the platform serves, from the platform's own origin", () => {
    render(<ApiReferencePage />);

    expect(screen.getByTestId("redoc")).toHaveAttribute(
      "data-spec-url",
      "/api/v1/admin/docs/doc.json",
    );
  });

  it("reads in the reader's theme rather than ReDoc's own", () => {
    // ReDoc ships one light theme. Handed it unchanged, a reader on the dark
    // theme gets near-black body copy on a near-black page.
    render(<ApiReferencePage />);
    const light = textColor();
    cleanup();

    useThemeStore.getState().setTheme("dark");
    render(<ApiReferencePage />);
    const dark = textColor();

    expect(light).not.toBe(dark);
    // Body copy has to be the light end of the ramp on the dark page and the
    // dark end on the light one, or the page is unreadable in one of them.
    expect(light).toBe(redocOptions(false).theme?.colors?.text?.primary);
    expect(dark).toBe(redocOptions(true).theme?.colors?.text?.primary);
  });

  it("offers the interactive reader beside it, in a tab of its own", () => {
    render(<ApiReferencePage />);

    const link = screen.getByRole("link", { name: /Swagger UI/ });
    // Swagger UI is served by the platform, outside the SPA: following it in
    // place would drop the portal, so it opens in a new tab.
    expect(link).toHaveAttribute("href", "/api/v1/admin/docs/index.html");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });
});

describe("where the reference is reached from", () => {
  it("is an administrator's section, and a route the shell renders", () => {
    const item = adminNavItems.find((i) => i.path === "/admin/api-reference");
    expect(item?.label).toBe("API Reference");
    expect(portalNavItems.some((i) => i.path === "/admin/api-reference")).toBe(false);
    // A path the shell does not recognize renders the not-found page, however
    // many nav items point at it.
    expect(isKnownRoute("/admin/api-reference")).toBe(true);
  });
});
