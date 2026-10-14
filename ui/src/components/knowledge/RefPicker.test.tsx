import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";

// The picker's own reads: the portal searches and the catalog's definitions.
// Each is stubbed so a test drives what a type's search returns.
let mockRefs: { urn: string; type: string; label: string; exists: boolean; source: string }[] = [];
const setRefs = vi.fn();
vi.mock("@/api/portal/hooks", () => ({
  useKnowledgePageRefs: () => ({ data: { refs: mockRefs } }),
  useSetKnowledgePageRefs: () => ({ mutate: setRefs, isPending: false }),
  useSearchAssets: (q: string) => ({
    data: q ? { data: [{ asset: { id: "a1", name: "Sales Dashboard" } }] } : undefined,
  }),
  useSearchCollections: () => ({ data: undefined }),
  useSearchKnowledgePages: () => ({ data: undefined }),
  useSearchMyPrompts: () => ({ data: undefined }),
}));

// The catalog's definitions, by kind. The stub filters the way the route
// does: the kinds asked for, whose name holds the text.
let catalogReadable = true;
let mockDefinitions: { reference: string; kind: string; name: string }[] = [];
const definitionCalls: { p: { kind?: string[]; q?: string; limit?: number }; enabled: boolean }[] = [];
vi.mock("@/api/portal/catalog", () => ({
  useCatalogConnections: () => ({ isSuccess: catalogReadable }),
  useDefinitions: (p: { kind?: string[]; q?: string; limit?: number }, enabled: boolean) => {
    definitionCalls.push({ p, enabled });
    if (!enabled) return { data: undefined };
    const q = (p.q ?? "").toLowerCase();
    return {
      data: mockDefinitions.filter(
        (d) => (p.kind ?? []).includes(d.kind) && d.name.toLowerCase().includes(q),
      ),
    };
  },
}));

import { RefPicker } from "./RefPicker";

// The type control is a Radix listbox: jsdom has no PointerEvent, so it opens on
// a key press rather than a pointer down (see src/test/setup.ts).
function openTypes() {
  fireEvent.keyDown(screen.getByLabelText("Reference type"), { key: "Enter" });
}

// pick selects a reference type by its display name and types a search query.
function pick(type: string, query: string) {
  openTypes();
  fireEvent.click(screen.getByRole("option", { name: type }));
  fireEvent.change(screen.getByPlaceholderText(/Search .* to reference/), {
    target: { value: query },
  });
}

beforeEach(() => {
  setRefs.mockClear();
  mockRefs = [];
  catalogReadable = true;
  definitionCalls.length = 0;
  mockDefinitions = [
    { reference: "mcp:glossary_term:8f3c1a94", kind: "glossary_term", name: "Net Revenue" },
    { reference: "mcp:tag:pii", kind: "tag", name: "PII" },
    { reference: "mcp:domain:c3d4e5f6", kind: "domain", name: "Finance" },
    { reference: "mcp:domain:a1b2c3d4", kind: "domain", name: "Marketing" },
  ];
});

describe("RefPicker", () => {
  it("attaches a glossary term by name, storing its reference", () => {
    // The whole point of #1159: an author selects "Net Revenue" and never sees
    // the id the catalog gave the term.
    render(<RefPicker pageId="kp1" />);
    pick("Glossary term", "net");

    fireEvent.click(screen.getByText("Net Revenue"));
    expect(setRefs).toHaveBeenCalledWith(["mcp:glossary_term:8f3c1a94"]);
  });

  it("attaches a tag and a domain by name too", () => {
    render(<RefPicker pageId="kp1" />);

    pick("Tag", "pii");
    fireEvent.click(screen.getByText("PII"));
    expect(setRefs).toHaveBeenLastCalledWith(["mcp:tag:pii"]);

    pick("Domain", "finance");
    fireEvent.click(screen.getByText("Finance"));
    expect(setRefs).toHaveBeenLastCalledWith(["mcp:domain:c3d4e5f6"]);
  });

  it("searches only the selected kind, by the text typed, a page at a time", () => {
    render(<RefPicker pageId="kp1" />);
    pick("Domain", "market");
    expect(screen.getByText("Marketing")).toBeInTheDocument();
    expect(screen.queryByText("Finance")).not.toBeInTheDocument();
    const enabled = definitionCalls.filter((c) => c.enabled);
    expect(enabled[enabled.length - 1]?.p).toEqual({
      kind: ["domain"],
      q: "market",
      limit: 8,
    });
  });

  it("reads no definitions while a portal type is selected", () => {
    render(<RefPicker pageId="kp1" />);
    pick("Asset", "sales");
    expect(definitionCalls.every((c) => !c.enabled)).toBe(true);
  });

  it("keeps the portal types storing mcp: references", () => {
    render(<RefPicker pageId="kp1" />);
    pick("Asset", "sales");
    fireEvent.click(screen.getByText("Sales Dashboard"));
    expect(setRefs).toHaveBeenCalledWith(["mcp:asset:a1"]);
  });

  it("preserves the references already attached when adding one", () => {
    mockRefs = [
      { urn: "mcp:tag:pii", type: "catalog", label: "PII", exists: true, source: "manual" },
    ];
    render(<RefPicker pageId="kp1" />);
    pick("Glossary term", "net");
    fireEvent.click(screen.getByText("Net Revenue"));
    expect(setRefs).toHaveBeenCalledWith(["mcp:tag:pii", "mcp:glossary_term:8f3c1a94"]);
  });

  it("refuses to attach the same entity twice", () => {
    mockRefs = [
      {
        urn: "mcp:glossary_term:8f3c1a94",
        type: "catalog",
        label: "Net Revenue",
        exists: true,
        source: "manual",
      },
    ];
    render(<RefPicker pageId="kp1" />);
    pick("Glossary term", "net");
    // The candidate is marked as already attached and its button is disabled,
    // so a second click cannot duplicate it. Scope to the candidate list: the
    // term also appears above as the chip for the reference already attached.
    const candidates = within(screen.getByRole("list"));
    expect(candidates.getByText("added")).toBeInTheDocument();
    fireEvent.click(candidates.getByText("Net Revenue"));
    expect(setRefs).not.toHaveBeenCalled();
  });

  it("offers no catalog types to a reader who cannot read the catalog", () => {
    // Otherwise the picker would show three types whose search can never return
    // a candidate.
    catalogReadable = false;
    render(<RefPicker pageId="kp1" />);
    openTypes();
    const options = screen.getAllByRole("option").map((o) => o.textContent);
    expect(options).toEqual(["Asset", "Collection", "Page", "Prompt"]);
  });

  it("removes an attached reference", () => {
    mockRefs = [
      { urn: "mcp:tag:pii", type: "catalog", label: "PII", exists: true, source: "manual" },
      {
        urn: "mcp:domain:c3d4e5f6",
        type: "catalog",
        label: "Finance",
        exists: true,
        source: "manual",
      },
    ];
    render(<RefPicker pageId="kp1" />);
    fireEvent.click(screen.getByLabelText("Remove PII"));
    expect(setRefs).toHaveBeenCalledWith(["mcp:domain:c3d4e5f6"]);
  });

  it("leaves promoted and inline references alone", () => {
    // The server replaces only source=manual, and the picker must send only
    // those: sending a promoted reference back as manual would relabel it.
    mockRefs = [
      { urn: "mcp:tag:pii", type: "catalog", label: "PII", exists: true, source: "manual" },
      {
        urn: "mcp:tag:certified",
        type: "catalog",
        label: "certified",
        exists: true,
        source: "promoted",
      },
    ];
    render(<RefPicker pageId="kp1" />);
    pick("Glossary term", "net");
    fireEvent.click(screen.getByText("Net Revenue"));
    expect(setRefs).toHaveBeenCalledWith(["mcp:tag:pii", "mcp:glossary_term:8f3c1a94"]);
  });
});
