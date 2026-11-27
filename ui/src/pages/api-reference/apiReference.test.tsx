import { describe, it, expect, afterEach, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

import { adminNavItems, portalNavItems } from "@/components/layout/sidebar/navItems";
import { isKnownRoute } from "@/lib/portalRoutes";
import { ApiReferencePage, REFERENCE_BASE_PATH } from "./ApiReferencePage";
import { redocOptions } from "./redocTheme";

// ReDoc stands in for itself here: what this page decides is which document it
// is pointed at, where ReDoc's own addresses start and what it is told about
// telemetry, and all three travel as props. The real component would pull a
// 1 MB bundle into every run of this file to prove the same things.
vi.mock("redoc", () => ({
  RedocStandalone: ({
    specUrl,
    basePath,
    options,
  }: {
    specUrl?: string;
    basePath?: string;
    options?: { disableTelemetry?: boolean };
  }) => (
    <div
      data-testid="redoc"
      data-spec-url={specUrl}
      data-base-path={basePath}
      data-telemetry={options?.disableTelemetry ? "off" : "on"}
    />
  ),
}));

afterEach(() => {
  cleanup();
});

describe("the API reference page", () => {
  it("renders the document the platform serves, from the platform's own origin", () => {
    render(<ApiReferencePage />);

    expect(screen.getByTestId("redoc")).toHaveAttribute(
      "data-spec-url",
      "/api/v1/admin/docs/doc.json",
    );
  });

  it("writes ReDoc's addresses under its own page, not from the site's root", () => {
    // ReDoc 3 gives every tag and operation an address. From "/", a reader
    // clicking "Gateway" would leave the portal for /gateway (#2769).
    render(<ApiReferencePage />);
    expect(screen.getByTestId("redoc")).toHaveAttribute(
      "data-base-path",
      "/portal/admin/api-reference",
    );
    expect(REFERENCE_BASE_PATH).toBe("/portal/admin/api-reference");
  });

  it("sends Redocly no telemetry", () => {
    // ReDoc 3 traces every page view to Redocly's collector unless told not to.
    render(<ApiReferencePage />);
    expect(screen.getByTestId("redoc")).toHaveAttribute("data-telemetry", "off");
    expect(redocOptions().disableTelemetry).toBe(true);
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
    // Each tag and operation ReDoc 3 links to is an address under the page,
    // opened directly from a bookmark or a reload (#2769).
    expect(isKnownRoute("/admin/api-reference/gateway")).toBe(true);
    expect(
      isKnownRoute("/admin/api-reference/gateway/paths/~1gateway~1%7Bconnection%7D~1invoke/post"),
    ).toBe(true);
  });
});
