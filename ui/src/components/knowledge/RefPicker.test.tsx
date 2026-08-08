import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { useEffect } from "react";

// The picker's own reads: the portal searches and the DataHub governance
// lookups. Each is stubbed so a test drives what a type's search returns.
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

let mockConnections: { name: string; writable: boolean }[] = [];
let mockTerms: { urn: string; name: string }[] = [];
let mockTags: { urn: string; name: string }[] = [];
let mockDomains: { urn: string; name: string }[] = [];
vi.mock("@/api/portal/datahub", () => ({
  useDataHubConnections: () => ({ data: mockConnections }),
  useGlossaryLookup: (conn: string, q: string) => ({ data: conn && q ? mockTerms : undefined }),
  useTagLookup: (conn: string, q: string) => ({ data: conn && q ? mockTags : undefined }),
  useDomainLookup: (conn: string, enabled: boolean) => ({
    data: conn && enabled ? mockDomains : undefined,
  }),
  MIN_SEARCH_LEN: 2,
}));

// The connection select is exercised in its own test. The stub keeps the one
// behavior the picker depends on: it selects a connection as soon as it renders,
// which is what the real control does when the current value names none.
vi.mock("./DataHubConnectionSelect", () => ({
  DataHubConnectionSelect: ({ value, onChange }: { value: string; onChange: (n: string) => void }) => {
    useEffect(() => {
      if (!value && mockConnections.length > 0) onChange(mockConnections[0]!.name);
    }, [value, onChange]);
    return <span>connection: {value || "none"}</span>;
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
  mockConnections = [{ name: "primary", writable: true }];
  mockTerms = [{ urn: "urn:li:glossaryTerm:8f3c1a94", name: "Net Revenue" }];
  mockTags = [{ urn: "urn:li:tag:pii", name: "PII" }];
  mockDomains = [
    { urn: "urn:li:domain:c3d4e5f6", name: "Finance" },
    { urn: "urn:li:domain:a1b2c3d4", name: "Marketing" },
  ];
});

describe("RefPicker", () => {
  it("attaches a glossary term by name, storing its URN", () => {
    // The whole point of #1159: an author selects "Net Revenue" and never sees
    // the generated key DataHub gave the term.
    render(<RefPicker pageId="kp1" />);
    pick("Glossary term", "net");

    fireEvent.click(screen.getByText("Net Revenue"));
    expect(setRefs).toHaveBeenCalledWith(["urn:li:glossaryTerm:8f3c1a94"]);
  });

  it("attaches a tag and a domain by name too", () => {
    render(<RefPicker pageId="kp1" />);

    pick("Tag", "pii");
    fireEvent.click(screen.getByText("PII"));
    expect(setRefs).toHaveBeenLastCalledWith(["urn:li:tag:pii"]);

    pick("Domain", "finance");
    fireEvent.click(screen.getByText("Finance"));
    expect(setRefs).toHaveBeenLastCalledWith(["urn:li:domain:c3d4e5f6"]);
  });

  it("filters domains client-side, since DataHub has no name-scoped domain search", () => {
    render(<RefPicker pageId="kp1" />);
    pick("Domain", "market");
    expect(screen.getByText("Marketing")).toBeInTheDocument();
    expect(screen.queryByText("Finance")).not.toBeInTheDocument();
  });

  it("keeps the portal types storing mcp: references", () => {
    render(<RefPicker pageId="kp1" />);
    pick("Asset", "sales");
    fireEvent.click(screen.getByText("Sales Dashboard"));
    expect(setRefs).toHaveBeenCalledWith(["mcp:asset:a1"]);
  });

  it("preserves the references already attached when adding one", () => {
    mockRefs = [
      { urn: "urn:li:tag:pii", type: "datahub", label: "PII", exists: true, source: "manual" },
    ];
    render(<RefPicker pageId="kp1" />);
    pick("Glossary term", "net");
    fireEvent.click(screen.getByText("Net Revenue"));
    expect(setRefs).toHaveBeenCalledWith(["urn:li:tag:pii", "urn:li:glossaryTerm:8f3c1a94"]);
  });

  it("refuses to attach the same entity twice", () => {
    mockRefs = [
      {
        urn: "urn:li:glossaryTerm:8f3c1a94",
        type: "datahub",
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

  it("asks for a connection only when a catalog type is selected", () => {
    // A governance entity belongs to one catalog; the portal's own entities
    // belong to none, so the connection control has nothing to say for them.
    render(<RefPicker pageId="kp1" />);
    expect(screen.queryByText(/^connection:/)).not.toBeInTheDocument();
    pick("Tag", "pii");
    expect(screen.getByText(/^connection:/)).toBeInTheDocument();
  });

  it("offers no catalog types when no DataHub connection exists", () => {
    // Otherwise the picker would show three types whose search can never return
    // a candidate on a database-only deployment.
    mockConnections = [];
    render(<RefPicker pageId="kp1" />);
    openTypes();
    const options = screen.getAllByRole("option").map((o) => o.textContent);
    expect(options).toEqual(["Asset", "Collection", "Page", "Prompt"]);
  });

  it("removes an attached reference", () => {
    mockRefs = [
      { urn: "urn:li:tag:pii", type: "datahub", label: "PII", exists: true, source: "manual" },
      {
        urn: "urn:li:domain:c3d4e5f6",
        type: "datahub",
        label: "Finance",
        exists: true,
        source: "manual",
      },
    ];
    render(<RefPicker pageId="kp1" />);
    fireEvent.click(screen.getByLabelText("Remove PII"));
    expect(setRefs).toHaveBeenCalledWith(["urn:li:domain:c3d4e5f6"]);
  });

  it("leaves promoted and inline references alone", () => {
    // The server replaces only source=manual, and the picker must send only
    // those: sending a promoted reference back as manual would relabel it.
    mockRefs = [
      { urn: "urn:li:tag:pii", type: "datahub", label: "PII", exists: true, source: "manual" },
      {
        urn: "urn:li:tag:certified",
        type: "datahub",
        label: "certified",
        exists: true,
        source: "promoted",
      },
    ];
    render(<RefPicker pageId="kp1" />);
    pick("Glossary term", "net");
    fireEvent.click(screen.getByText("Net Revenue"));
    expect(setRefs).toHaveBeenCalledWith(["urn:li:tag:pii", "urn:li:glossaryTerm:8f3c1a94"]);
  });
});
