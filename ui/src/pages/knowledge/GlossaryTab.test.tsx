import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { DefinitionSearch, DefinitionSummary, DefinitionView } from "@/api/portal/catalog";

// The catalog hooks are stood in by fixtures: the list a search returns, the
// definitions by reference, and every write recorded.
const fx = vi.hoisted(() => ({
  list: [] as DefinitionSummary[],
  byRef: {} as Record<string, DefinitionView>,
  searches: [] as DefinitionSearch[],
  creates: [] as unknown[],
  deletes: [] as unknown[],
  writes: [] as unknown[],
  deleteError: null as unknown,
}));

const query = (data: unknown) => ({ data, isLoading: false, isError: data === undefined, isFetching: false });
const mutation = (log: unknown[], result: () => unknown = () => undefined, error: () => unknown = () => null) => ({
  mutate: (v: unknown, opts?: { onSuccess?: (r: unknown) => void }) => {
    log.push(v);
    if (!error()) opts?.onSuccess?.(result());
  },
  isPending: false,
  isError: error() != null,
  error: error(),
});

vi.mock("@/api/portal/catalog", () => ({
  MIN_SEARCH_LEN: 2,
  useDefinitions: (p: DefinitionSearch) => {
    fx.searches.push(p);
    return query(fx.list);
  },
  useDefinition: (ref: string | null) => query(ref ? fx.byRef[ref] : undefined),
  useCatalogHistory: () => query([]),
  useRollback: () => mutation([]),
  useWriteAspect: () => mutation(fx.writes),
  useCreateDefinition: () => mutation(fx.creates, () => ({ reference: "mcp:glossary_term:new" })),
  useDeleteDefinition: () => mutation(fx.deletes, () => undefined, () => fx.deleteError),
}));
// Backlinks read the knowledge-page index, which has tests of its own.
vi.mock("@/components/knowledge/KnowledgeBacklinks", () => ({
  KnowledgeBacklinks: ({ urn }: { urn: string }) => <div>backlinks for {urn}</div>,
}));
vi.mock("@/components/MarkdownEditor", () => ({
  MarkdownEditor: ({ value, onChange, label }: { value: string; onChange: (v: string) => void; label?: string }) => (
    <textarea aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

import { GlossaryTab } from "./GlossaryTab";
import { DefinitionsTab } from "./DefinitionsTab";

const node: DefinitionSummary = {
  reference: "mcp:glossary_node:n1",
  kind: "glossary_node",
  slug: "finance",
  name: "Finance",
  children: 1,
};
const term: DefinitionSummary = {
  reference: "mcp:glossary_term:t1",
  kind: "glossary_term",
  slug: "revenue",
  name: "Revenue",
  description: "Gross sales.",
  parent: node.reference,
  children: 0,
};

function view(s: DefinitionSummary, over: Partial<DefinitionView> = {}): DefinitionView {
  return {
    id: s.slug,
    reference: s.reference,
    kind: s.kind,
    slug: s.slug,
    name: s.name,
    parent: s.parent,
    aspects: { description: s.description },
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    children: [],
    uses: [],
    uses_withheld: 0,
    more_uses: false,
    writable: true,
    ...over,
  };
}

beforeEach(() => {
  fx.list = [node];
  fx.byRef = {
    [node.reference]: view(node, { children: [term] }),
    [term.reference]: view(term, {
      uses: [
        {
          reference: "mcp:column:(trino,acme,analytics.public.daily_sales,revenue)",
          dataset: "mcp:dataset:(trino,acme,analytics.public.daily_sales)",
          name: "daily_sales",
          column: "revenue",
          connection: "acme",
        },
      ],
      uses_withheld: 2,
    }),
  };
  fx.searches = [];
  fx.creates = [];
  fx.deletes = [];
  fx.writes = [];
  fx.deleteError = null;
  window.history.replaceState(null, "", "/knowledge/catalog#glossary");
});

describe("GlossaryTab", () => {
  it("lists the top of the glossary, and every entry matching a filter", () => {
    render(<GlossaryTab canEdit />);
    expect(screen.getByText("Finance")).toBeInTheDocument();
    expect(screen.getByText("1 entry")).toBeInTheDocument();
    expect(fx.searches[fx.searches.length - 1]).toMatchObject({ roots: true, q: "" });
  });

  it("opens a node to what is filed under it, and a term to what carries it", () => {
    const onNavigate = vi.fn();
    render(<GlossaryTab canEdit onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: /Finance/ }));
    fireEvent.click(screen.getByRole("button", { name: /Revenue/ }));
    expect(new URLSearchParams(window.location.search).get("ref")).toBe(term.reference);
    expect(screen.getByText("2 more on connections your persona does not reach.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("link", { name: /daily_sales/ }));
    expect(onNavigate).toHaveBeenCalledWith(
      `/knowledge/catalog?ref=${encodeURIComponent("mcp:dataset:(trino,acme,analytics.public.daily_sales)")}#tables`,
    );
    fireEvent.click(screen.getByRole("button", { name: "Up to the parent node" }));
    expect(new URLSearchParams(window.location.search).get("ref")).toBe(node.reference);
  });

  it("opens the deep-linked term and goes back to the glossary", () => {
    window.history.replaceState(null, "", `/knowledge/catalog?ref=${encodeURIComponent(term.reference)}#glossary`);
    render(<GlossaryTab canEdit />);
    expect(screen.getByRole("heading", { name: "Revenue" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Back to the glossary/ }));
    expect(window.location.search).toBe("");
    expect(screen.getByText("Finance")).toBeInTheDocument();
  });

  it("creates a term at the top of the glossary and opens it", () => {
    render(<GlossaryTab canEdit />);
    fireEvent.click(screen.getByRole("button", { name: /New entry/ }));
    fireEvent.change(screen.getByLabelText("New glossary term name"), { target: { value: "Churn" } });
    fireEvent.change(screen.getByLabelText("New glossary term description"), { target: { value: "Lost customers." } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(fx.creates).toEqual([{ kind: "glossary_term", name: "Churn", parent: undefined, description: "Lost customers." }]);
    expect(new URLSearchParams(window.location.search).get("ref")).toBe("mcp:glossary_term:new");
  });

  it("files a new entry under the node it is added from", () => {
    window.history.replaceState(null, "", `/knowledge/catalog?ref=${encodeURIComponent(node.reference)}#glossary`);
    render(<GlossaryTab canEdit />);
    fireEvent.click(screen.getByRole("button", { name: /Add entry/ }));
    fireEvent.change(screen.getByLabelText("New glossary term name"), { target: { value: "Refund" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(fx.creates).toEqual([{ kind: "glossary_term", name: "Refund", parent: node.reference, description: undefined }]);
  });

  it("shows the refusal when a term still in use is deleted", () => {
    fx.deleteError = { message: "in use" };
    window.history.replaceState(null, "", `/knowledge/catalog?ref=${encodeURIComponent(term.reference)}#glossary`);
    render(<GlossaryTab canEdit />);
    fireEvent.click(screen.getByRole("button", { name: /Delete glossary term/ }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    expect(fx.deletes).toEqual([term.reference]);
    expect(screen.getByText("Update failed.")).toBeInTheDocument();
  });

  it("offers no create, edit or delete to a reader who may not write", () => {
    fx.byRef[term.reference] = view(term, { writable: false });
    render(<GlossaryTab canEdit={false} />);
    expect(screen.queryByRole("button", { name: /New entry/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Finance/ }));
    fireEvent.click(screen.getByRole("button", { name: /Revenue/ }));
    expect(screen.queryByRole("button", { name: /Delete/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit description" })).not.toBeInTheDocument();
  });
});

describe("DefinitionsTab", () => {
  const tag: DefinitionSummary = { reference: "mcp:tag:pii", kind: "tag", slug: "pii", name: "pii", children: 0 };

  beforeEach(() => {
    fx.list = [tag];
    fx.byRef = { [tag.reference]: view(tag) };
    window.history.replaceState(null, "", "/knowledge/catalog#tags");
  });

  it("lists one kind and says an undocumented entry has no description", () => {
    render(<DefinitionsTab sub="tags" canEdit />);
    expect(fx.searches[fx.searches.length - 1]).toMatchObject({ kind: ["tag"] });
    expect(screen.getByText("No description")).toBeInTheDocument();
  });

  it("edits an entry's description through its own reference", () => {
    render(<DefinitionsTab sub="tags" canEdit />);
    fireEvent.click(screen.getByRole("button", { name: /pii/ }));
    fireEvent.click(screen.getByRole("button", { name: "Edit description" }));
    fireEvent.change(screen.getByLabelText("Description of pii"), { target: { value: "Personal data." } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(fx.writes).toEqual([{ reference: "mcp:tag:pii", aspect: "description", value: "Personal data.", expect: null }]);
  });

  it("creates a domain from the Domains tab", () => {
    fx.list = [];
    render(<DefinitionsTab sub="domains" canEdit />);
    expect(screen.getByText("No domains have been defined.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /New domain/ }));
    fireEvent.change(screen.getByLabelText("New domain name"), { target: { value: "Sales" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(fx.creates).toEqual([{ kind: "domain", name: "Sales", parent: undefined, description: undefined }]);
  });

  it("opens the deep-linked data product", () => {
    const product: DefinitionSummary = {
      reference: "mcp:data_product:p1",
      kind: "data_product",
      slug: "daily-revenue",
      name: "Daily revenue",
      children: 0,
    };
    fx.byRef[product.reference] = view(product);
    window.history.replaceState(null, "", `/knowledge/catalog?ref=${encodeURIComponent(product.reference)}#data-products`);
    render(<DefinitionsTab sub="data_products" canEdit />);
    expect(screen.getByRole("heading", { name: "Daily revenue" })).toBeInTheDocument();
    expect(screen.queryByText("Used by")).not.toBeInTheDocument();
  });
});
