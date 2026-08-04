import { useState } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

// The section is the container: what it owns is the connection, the inner tab,
// and the URL contract. Its four inner tabs are stood in so those concerns are
// tested without their data layers. Each stand-in carries internal state, so a
// remount (which is how a connection change resets them) is observable.
function Stub({ label }: { label: string }) {
  const [opened, setOpened] = useState(false);
  return (
    <div>
      <span>{label} body</span>
      {opened ? (
        <span>{label} detail</span>
      ) : (
        <button onClick={() => setOpened(true)}>open {label}</button>
      )}
    </div>
  );
}

vi.mock("./CatalogTab", () => ({
  CatalogTab: ({ conn }: { conn: string }) => <Stub label={`tables:${conn}`} />,
}));
vi.mock("./ContextDocsTab", () => ({
  ContextDocsTab: ({ conn }: { conn: string }) => <Stub label={`docs:${conn}`} />,
}));
vi.mock("./TagsTab", () => ({
  TagsTab: ({ conn }: { conn: string }) => <Stub label={`tags:${conn}`} />,
}));
vi.mock("./DomainsTab", () => ({
  DomainsTab: ({ conn }: { conn: string }) => <Stub label={`domains:${conn}`} />,
}));
// Stand in a picker that switches connection, so the section's reaction to a
// change is testable without the real select's data fetch.
vi.mock("@/components/knowledge/DataHubConnectionSelect", () => ({
  DataHubConnectionSelect: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (c: string) => void;
  }) => (
    <div>
      <span>conn={value || "none"}</span>
      <button onClick={() => onChange("primary")}>pick primary</button>
      <button onClick={() => onChange("other")}>pick other</button>
    </div>
  ),
}));

import { CatalogSection } from "./CatalogSection";

const path = () => window.location.pathname + window.location.search + window.location.hash;

// jsdom provides no localStorage, and the section's persistence is a real
// behaviour (the selection has to outlive a refresh), so stand one in rather
// than leave that path untested.
const store = new Map<string, string>();
globalThis.localStorage = {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
  removeItem: (k: string) => void store.delete(k),
  clear: () => store.clear(),
  key: (i: number) => [...store.keys()][i] ?? null,
  get length() {
    return store.size;
  },
} satisfies Storage;

beforeEach(() => {
  store.clear();
  window.history.replaceState(null, "", "/knowledge/catalog");
});

describe("CatalogSection", () => {
  it("opens Tables by default and renders one connection picker for the section", () => {
    render(<CatalogSection />);
    fireEvent.click(screen.getByRole("button", { name: "pick primary" }));

    expect(screen.getByText("tables:primary body")).toBeInTheDocument();
    expect(screen.queryByText("docs:primary body")).not.toBeInTheDocument();
    expect(screen.queryByText("tags:primary body")).not.toBeInTheDocument();
    expect(screen.queryByText("domains:primary body")).not.toBeInTheDocument();
    // One picker for the whole section, not one per inner tab.
    expect(screen.getAllByRole("button", { name: "pick primary" })).toHaveLength(1);
  });

  it("opens the inner tab the hash addresses", () => {
    render(<CatalogSection initialSub="context-docs" />);
    fireEvent.click(screen.getByRole("button", { name: "pick primary" }));
    expect(screen.getByText("docs:primary body")).toBeInTheDocument();
  });

  it("opens Domains from its hash", () => {
    render(<CatalogSection initialSub="domains" />);
    fireEvent.click(screen.getByRole("button", { name: "pick primary" }));
    expect(screen.getByText("domains:primary body")).toBeInTheDocument();
  });

  it("falls back to Tables for a hash that addresses nothing", () => {
    render(<CatalogSection initialSub="glossary" />);
    fireEvent.click(screen.getByRole("button", { name: "pick primary" }));
    expect(screen.getByText("tables:primary body")).toBeInTheDocument();
  });

  it("records the inner tab in the hash without leaving the route or dropping a deep link", () => {
    window.history.replaceState(null, "", "/knowledge/catalog?urn=urn:li:tag:pii");
    render(<CatalogSection />);
    fireEvent.click(screen.getByRole("button", { name: "pick primary" }));

    fireEvent.click(screen.getByRole("button", { name: "Tags" }));
    expect(path()).toBe("/knowledge/catalog?urn=urn:li:tag:pii#tags");
    fireEvent.click(screen.getByRole("button", { name: "Context Docs" }));
    expect(path()).toBe("/knowledge/catalog?urn=urn:li:tag:pii#context-docs");
    fireEvent.click(screen.getByRole("button", { name: "Domains" }));
    expect(path()).toBe("/knowledge/catalog?urn=urn:li:tag:pii#domains");
    fireEvent.click(screen.getByRole("button", { name: "Tables" }));
    expect(path()).toBe("/knowledge/catalog?urn=urn:li:tag:pii#tables");
  });

  it("holds the connection across an inner tab switch", () => {
    render(<CatalogSection />);
    fireEvent.click(screen.getByRole("button", { name: "pick other" }));
    fireEvent.click(screen.getByRole("button", { name: "Tags" }));

    // The whole point of nesting: the sibling inherits the connection instead of
    // re-picking one through top-level state.
    expect(screen.getByText("tags:other body")).toBeInTheDocument();
    expect(screen.getByText("conn=other")).toBeInTheDocument();
  });

  it("persists the connection so a refresh does not reset it", () => {
    const first = render(<CatalogSection />);
    fireEvent.click(screen.getByRole("button", { name: "pick other" }));
    first.unmount();

    render(<CatalogSection />);
    expect(screen.getByText("conn=other")).toBeInTheDocument();
    expect(screen.getByText("tables:other body")).toBeInTheDocument();
  });

  it("renders no body until a connection is selected", () => {
    render(<CatalogSection />);
    expect(screen.getByText("conn=none")).toBeInTheDocument();
    expect(screen.queryByText(/ body$/)).not.toBeInTheDocument();
  });

  it("resets the open inner tab when the connection changes", () => {
    render(<CatalogSection initialSub="tags" />);
    fireEvent.click(screen.getByRole("button", { name: "pick primary" }));
    fireEvent.click(screen.getByRole("button", { name: "open tags:primary" }));
    expect(screen.getByText("tags:primary detail")).toBeInTheDocument();

    // An open tag belongs to one connection, so it must not survive a switch or
    // its detail would be read from the new one.
    fireEvent.click(screen.getByRole("button", { name: "pick other" }));
    expect(screen.queryByText("tags:primary detail")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "open tags:other" })).toBeInTheDocument();
  });
});
