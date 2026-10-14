import { useState } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

// The section is the container: what it owns is the inner tab, the URL
// contract and whether the reader may edit. Its inner tabs are stood in so
// those concerns are tested without their data layers. Each stand-in carries
// internal state, so a remount is observable.
function Stub({ label }: { label: string }) {
  const [opened, setOpened] = useState(false);
  return (
    <div>
      <span>{label} body</span>
      {opened ? <span>{label} detail</span> : <button onClick={() => setOpened(true)}>open {label}</button>}
    </div>
  );
}

vi.mock("./CatalogTab", () => ({ CatalogTab: () => <Stub label="tables" /> }));
vi.mock("./GlossaryTab", () => ({
  GlossaryTab: ({ canEdit }: { canEdit: boolean }) => <Stub label={`glossary:${canEdit}`} />,
}));
vi.mock("./DefinitionsTab", () => ({
  DefinitionsTab: ({ sub, canEdit }: { sub: string; canEdit: boolean }) => (
    <Stub label={`${sub}:${canEdit}`} />
  ),
}));
const writable = vi.hoisted(() => ({ current: true }));
vi.mock("@/api/portal/catalog", () => ({
  useCatalogConnections: () => ({ data: { connections: [], writable: writable.current } }),
}));

import { CatalogSection } from "./CatalogSection";

const path = () => window.location.pathname + window.location.search + window.location.hash;

// The inner tab bar is a Radix tablist (SubTabBar over the shared Tabs
// primitive); its triggers carry role "tab" and activate on mousedown.
const clickTab = (name: string) => fireEvent.mouseDown(screen.getByRole("tab", { name }));

beforeEach(() => {
  writable.current = true;
  window.history.replaceState(null, "", "/knowledge/catalog");
});

describe("CatalogSection", () => {
  it("opens Tables by default", () => {
    render(<CatalogSection />);
    expect(screen.getByText("tables body")).toBeInTheDocument();
    expect(screen.queryByText(/glossary:/)).not.toBeInTheDocument();
  });

  it("opens the inner tab the hash addresses", () => {
    render(<CatalogSection initialSub="data-products" />);
    expect(screen.getByText("data_products:true body")).toBeInTheDocument();
  });

  it("falls back to Tables for a hash that addresses nothing", () => {
    // The retired Context Docs tab's hash among them: its documents now live
    // on the dataset page.
    render(<CatalogSection initialSub="context-docs" />);
    expect(screen.getByText("tables body")).toBeInTheDocument();
  });

  it("records the inner tab in the hash and drops the entity the old tab had open", () => {
    window.history.replaceState(null, "", "/knowledge/catalog?ref=mcp%3Atag%3Apii#tags");
    render(<CatalogSection initialSub="tags" />);
    clickTab("Glossary");
    expect(path()).toBe("/knowledge/catalog#glossary");
    clickTab("Domains");
    expect(path()).toBe("/knowledge/catalog#domains");
    clickTab("Data products");
    expect(path()).toBe("/knowledge/catalog#data-products");
    clickTab("Tables");
    expect(path()).toBe("/knowledge/catalog#tables");
  });

  it("resets an open entity when the tab changes", () => {
    render(<CatalogSection initialSub="tags" />);
    fireEvent.click(screen.getByRole("button", { name: "open tags:true" }));
    expect(screen.getByText("tags:true detail")).toBeInTheDocument();
    clickTab("Domains");
    clickTab("Tags");
    expect(screen.queryByText("tags:true detail")).not.toBeInTheDocument();
  });

  it("passes whether the reader may edit the catalog to the definition tabs", () => {
    writable.current = false;
    render(<CatalogSection initialSub="glossary" />);
    expect(screen.getByText("glossary:false body")).toBeInTheDocument();
  });
});
