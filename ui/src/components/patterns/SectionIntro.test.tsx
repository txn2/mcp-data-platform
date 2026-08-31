import { describe, it, expect, beforeEach, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

import { portalNavItems } from "@/components/layout/sidebar/navItems";
import { SectionIntro } from "./SectionIntro";
import { SECTION_INTROS, sectionIntroFor, sectionIntroPath } from "./sectionIntros";

describe("section intro copy", () => {
  it("covers every reader-facing nav section except Settings", () => {
    const covered = new Set(SECTION_INTROS.map((i) => i.path));
    const missing = portalNavItems
      .filter((item) => item.path !== "/settings")
      .filter((item) => !covered.has(item.path))
      .map((item) => item.label);
    expect(missing, "nav sections with no intro copy").toEqual([]);
  });

  it("names no section the nav does not have, and never Settings", () => {
    const navPaths = new Set(portalNavItems.map((item) => item.path));
    for (const intro of SECTION_INTROS) {
      expect(navPaths.has(intro.path), intro.path).toBe(true);
      expect(intro.path).not.toBe("/settings");
    }
  });

  it("gives every section its own storage key and non-empty copy", () => {
    const keys = new Set<string>();
    for (const intro of SECTION_INTROS) {
      expect(keys.has(intro.storageKey), intro.storageKey).toBe(false);
      keys.add(intro.storageKey);
      expect(intro.summary.length, intro.path).toBeGreaterThan(0);
      expect(intro.belongs.length, intro.path).toBeGreaterThan(0);
      expect(intro.notHere.length, intro.path).toBeGreaterThan(0);
    }
  });

  it("keeps the Knowledge header on the key a reader has already answered", () => {
    // A reader who collapsed the lifecycle header before #1570 must not get it
    // back, so the Knowledge section reuses that key rather than minting one.
    expect(sectionIntroFor("/knowledge")?.storageKey).toBe("knowledge.lifecycle.expanded");
  });

  it("has Assets and Resources each point at the other", () => {
    expect(sectionIntroFor("/")?.notHere).toContain("Resource");
    expect(sectionIntroFor("/resources")?.notHere).toContain("Asset");
  });
});

describe("sectionIntroPath", () => {
  it("heads every listing surface of a section with that section's intro", () => {
    const cases: [string, string][] = [
      ["/", "/"],
      ["/collections", "/"],
      ["/prompts", "/prompts"],
      ["/scripts", "/scripts"],
      ["/resources", "/resources"],
      ["/resources/lib/user-1", "/resources"],
      ["/scratch-tables", "/scratch-tables"],
      ["/feedback", "/feedback"],
      ["/knowledge", "/knowledge"],
      ["/knowledge/pages", "/knowledge"],
      ["/knowledge/catalog", "/knowledge"],
      ["/apis", "/apis"],
      ["/activity", "/activity"],
      ["/activity/sessions", "/activity"],
      ["/activity/calls", "/activity"],
    ];
    for (const [route, section] of cases) {
      expect(sectionIntroPath(route), route).toBe(section);
    }
  });

  it("leaves the detail pages beneath a section to the thing the reader opened", () => {
    for (const route of [
      "/assets/ast-1",
      "/shared/assets/ast-1",
      "/collections/col-1",
      "/collections/col-1/edit",
      "/prompts/pr-1",
      // One knowledge page open is a detail view, even though the hub renders it.
      "/knowledge/pages/kp-1",
      "/scripts/sc-1",
      "/scratch-tables/tbl-1",
      "/activity/sessions/ses-1",
      "/activity/calls/call-1",
      "/settings",
      "/admin/resources",
      "/nope",
    ]) {
      expect(sectionIntroPath(route), route).toBeNull();
    }
  });
});

// The test environment has no localStorage of its own, which is also the state
// a private window and a browser set to block site data leave the page in.
function withStorage(store: Map<string, string>) {
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
    removeItem: (k: string) => void store.delete(k),
    clear: () => store.clear(),
  });
}

describe("SectionIntro", () => {
  let store: Map<string, string>;

  beforeEach(() => {
    store = new Map();
    withStorage(store);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("renders nothing for a section with no intro", () => {
    const { container } = render(<SectionIntro route="/settings" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("opens expanded for a reader who has never opened the section", () => {
    render(<SectionIntro route="/resources" />);
    expect(screen.getByRole("button", { name: /hide/i })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText(/Brand material/)).toBeInTheDocument();
    expect(screen.getByTestId("section-intro-detail")).toHaveAttribute("aria-hidden", "false");
  });

  it("keeps the summary when collapsed, and stays collapsed on the next visit", () => {
    const first = render(<SectionIntro route="/resources" />);
    fireEvent.click(screen.getByRole("button", { name: /hide/i }));

    expect(store.get("portal.intro.resources")).toBe("0");
    expect(screen.getByRole("button")).toHaveAttribute("aria-expanded", "false");
    // The one-line summary survives the collapse; only the disclosure closes,
    // and closed means out of the reading order, not merely clipped.
    expect(screen.getByText("Files you give agents and scripts to work from.")).toBeInTheDocument();
    expect(screen.getByTestId("section-intro-detail")).toHaveAttribute("aria-hidden", "true");
    first.unmount();

    render(<SectionIntro route="/resources" />);
    expect(screen.getByRole("button")).toHaveAttribute("aria-expanded", "false");
  });

  it("reads each section's own choice when the reader moves between them", () => {
    // The shell keeps this element in one place, so a client-side move is a
    // re-render with a new section rather than a remount.
    const view = render(<SectionIntro route="/resources" />);
    fireEvent.click(screen.getByRole("button", { name: /hide/i }));
    expect(screen.getByRole("button")).toHaveAttribute("aria-expanded", "false");

    view.rerender(<SectionIntro route="/prompts" />);
    expect(screen.getByRole("button")).toHaveAttribute("aria-expanded", "true");
    expect(store.has("portal.intro.prompts")).toBe(false);

    view.rerender(<SectionIntro route="/resources" />);
    expect(screen.getByRole("button")).toHaveAttribute("aria-expanded", "false");
  });

  it("reopens a section the reader expands again", () => {
    store.set("portal.intro.apis", "0");
    render(<SectionIntro route="/apis" />);
    fireEvent.click(screen.getByRole("button"));
    expect(store.get("portal.intro.apis")).toBe("1");
    expect(screen.getByRole("button", { name: /hide/i })).toHaveAttribute("aria-expanded", "true");
  });

  it("draws the lifecycle pipeline as the Knowledge section's summary", () => {
    render(<SectionIntro route="/knowledge" />);
    // Each stage names itself in the pipeline and again in the prose beneath it.
    for (const stage of ["Memory", "Insight", "Knowledge"]) {
      expect(screen.getAllByText(stage).length, stage).toBeGreaterThan(0);
    }
    expect(screen.getByText("captured automatically")).toBeInTheDocument();
    expect(screen.getByText(/Everything the platform learns is a/)).toBeInTheDocument();
    // The section keeps its own disclosure wording.
    fireEvent.click(screen.getByRole("button", { name: /hide/i }));
    expect(screen.getByRole("button", { name: /show what this section is for/i })).toHaveTextContent(
      "How it works",
    );
  });

  it("renders with storage unavailable, as a private window has it", () => {
    vi.stubGlobal("localStorage", undefined);
    render(<SectionIntro route="/feedback" />);
    const toggle = screen.getByRole("button");
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
  });
});
