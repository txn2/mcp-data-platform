import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import type { Change, Dataset, DatasetSummary, DefinitionSummary } from "@/api/portal/catalog";

// The catalog hooks are stood in by fixtures: the list, one dataset, the
// definitions a picker searches and the history, with each write recorded so
// a test asserts exactly what the page sends.
const fx = vi.hoisted(() => ({
  results: [] as DatasetSummary[],
  dataset: undefined as Dataset | undefined,
  definitions: [] as DefinitionSummary[],
  history: [] as Change[],
  writes: [] as unknown[],
  rollbacks: [] as unknown[],
  searches: [] as unknown[],
}));

const query = (data: unknown) => ({ data, isLoading: false, isError: false, isFetching: false });
const mutation = (log: unknown[]) => ({
  mutate: (v: unknown, opts?: { onSuccess?: () => void }) => {
    log.push(v);
    opts?.onSuccess?.();
  },
  isPending: false,
  isError: false,
  error: null,
});

vi.mock("@/api/portal/catalog", () => ({
  MIN_SEARCH_LEN: 2,
  useCatalogConnections: () =>
    query({
      connections: [
        { kind: "trino", name: "acme" },
        { kind: "api", name: "billing" },
      ],
      writable: true,
    }),
  useDatasetSearch: (p: unknown) => {
    fx.searches.push(p);
    return query(fx.results);
  },
  useDataset: (ref: string | null) => query(ref ? fx.dataset : undefined),
  useDefinitions: () => query(fx.definitions),
  useCatalogHistory: () => query(fx.history),
  useSchemaDocument: () => query(undefined),
  useWriteAspect: () => mutation(fx.writes),
  useRollback: () => mutation(fx.rollbacks),
}));

// The markdown editor is CodeMirror, which jsdom cannot drive; a textarea
// stands in with the same value/onChange/label contract.
// Backlinks read the knowledge-page index, which has tests of its own.
vi.mock("@/components/knowledge/KnowledgeBacklinks", () => ({
  KnowledgeBacklinks: ({ urn }: { urn: string }) => <div>backlinks for {urn}</div>,
}));
vi.mock("@/components/MarkdownEditor", () => ({
  MarkdownEditor: ({ value, onChange, label }: { value: string; onChange: (v: string) => void; label?: string }) => (
    <textarea aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

import { CatalogTab } from "./CatalogTab";

const REF = "mcp:dataset:(trino,acme,analytics.public.daily_sales)";

function dataset(over: Partial<Dataset> = {}): Dataset {
  return {
    id: "1",
    reference: REF,
    name: "daily_sales",
    type: "table",
    columns: [
      { name: "revenue", data_type: "decimal(12,2)", ordinal: 2, nullable: true, aspects: { tags: ["finance"] } },
      { name: "sale_date", data_type: "date", ordinal: 1, nullable: false, aspects: {} },
    ],
    aspects: {
      description: "Daily sales by store.",
      tags: ["certified"],
      glossary_terms: ["mcp:glossary_term:t1"],
      domain: "mcp:domain:d1",
      owners: [{ owner: "sarah@example.com", type: "technical_owner" }],
    },
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    upstream: [
      {
        source: "mcp:dataset:(trino,acme,raw.events.clickstream)",
        target: REF,
        writer_kind: "query",
        written_at: "2026-10-01T00:00:00Z",
        depth: 1,
      },
    ],
    downstream: [],
    names: { "mcp:glossary_term:t1": "Revenue", "mcp:domain:d1": "Sales" },
    writable: true,
    ...over,
  };
}

beforeEach(() => {
  fx.results = [
    {
      reference: REF,
      connection_kind: "trino",
      connection: "acme",
      path: "analytics.public.daily_sales",
      name: "daily_sales",
      type: "table",
      description: "Daily sales by store.",
      tags: ["certified"],
    },
  ];
  fx.dataset = dataset();
  fx.definitions = [];
  fx.history = [];
  fx.writes = [];
  fx.rollbacks = [];
  fx.searches = [];
  window.history.replaceState(null, "", "/knowledge/catalog#tables");
});

describe("CatalogTab list", () => {
  it("lists the datasets the search returns and opens one, recording it in the URL", () => {
    render(<CatalogTab />);
    fireEvent.click(screen.getByRole("button", { name: /daily_sales/ }));
    expect(screen.getByRole("heading", { name: "daily_sales" })).toBeInTheDocument();
    expect(new URLSearchParams(window.location.search).get("ref")).toBe(REF);
  });

  it("says the catalog is empty rather than showing nothing", () => {
    fx.results = [];
    render(<CatalogTab />);
    expect(screen.getByText(/The catalog holds no tables yet/)).toBeInTheDocument();
  });

  it("filters by connection when the reader picks one", () => {
    render(<CatalogTab />);
    expect(screen.getByRole("combobox", { name: "Connection" })).toBeInTheDocument();
    expect(fx.searches[fx.searches.length - 1]).toMatchObject({ connection_kind: "", connection: "" });
  });
});

describe("CatalogTab dataset page", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", `/knowledge/catalog?ref=${encodeURIComponent(REF)}#tables`);
  });

  it("opens the deep-linked dataset with its curation, names resolved", () => {
    render(<CatalogTab />);
    expect(screen.getByText("Daily sales by store.")).toBeInTheDocument();
    expect(screen.getByText("certified")).toBeInTheDocument();
    expect(screen.getAllByText("Revenue").length).toBeGreaterThan(0);
    expect(screen.getByText("Sales")).toBeInTheDocument();
    expect(screen.getByText("sarah@example.com")).toBeInTheDocument();
  });

  it("lists columns in their table order", () => {
    render(<CatalogTab />);
    const cells = screen.getAllByRole("row").map((r) => within(r).queryAllByRole("cell")[0]?.textContent);
    expect(cells.filter(Boolean)).toEqual(["sale_date", "revenue"]);
  });

  it("writes an edited description to the dataset's description aspect", () => {
    render(<CatalogTab />);
    fireEvent.click(screen.getAllByRole("button", { name: "Edit description" })[0]!);
    fireEvent.change(screen.getByLabelText("Dataset description"), { target: { value: "One row per store per day." } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(fx.writes).toEqual([
      { reference: REF, aspect: "description", value: "One row per store per day.", expect: "Daily sales by store." },
    ]);
  });

  it("removes a tag by rewriting the tags aspect without it", () => {
    render(<CatalogTab />);
    fireEvent.click(screen.getByRole("button", { name: "Remove tag certified" }));
    expect(fx.writes).toEqual([{ reference: REF, aspect: "tags", value: null, expect: ["certified"] }]);
  });

  it("adds a tag to a column through the column's own reference", () => {
    fx.definitions = [
      { reference: "mcp:tag:pii", kind: "tag", slug: "pii", name: "pii", children: 0 },
    ];
    render(<CatalogTab />);
    fireEvent.click(screen.getByRole("button", { name: "Edit column revenue" }));
    const input = screen.getByLabelText("Add tag on revenue");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "pii" } });
    fireEvent.click(screen.getByRole("button", { name: /^pii/ }));
    expect(fx.writes).toEqual([
      {
        reference: "mcp:column:(trino,acme,analytics.public.daily_sales,revenue)",
        aspect: "tags",
        value: ["finance", "pii"],
        expect: ["finance"],
      },
    ]);
  });

  it("offers a typed name no tag has yet as a new tag", () => {
    render(<CatalogTab />);
    const input = screen.getByLabelText("Add tag");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "gold" } });
    fireEvent.click(screen.getByRole("button", { name: /gold.*new tag/ }));
    expect(fx.writes).toEqual([{ reference: REF, aspect: "tags", value: ["certified", "gold"], expect: ["certified"] }]);
  });

  it("adds a glossary term by its reference", () => {
    fx.definitions = [
      { reference: "mcp:glossary_term:t2", kind: "glossary_term", slug: "net-sales", name: "Net Sales", children: 0 },
    ];
    render(<CatalogTab />);
    const input = screen.getByLabelText("Add glossary term");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "net" } });
    fireEvent.click(screen.getByRole("button", { name: /Net Sales/ }));
    expect(fx.writes).toEqual([
      {
        reference: REF,
        aspect: "glossary_terms",
        value: ["mcp:glossary_term:t1", "mcp:glossary_term:t2"],
        expect: ["mcp:glossary_term:t1"],
      },
    ]);
  });

  it("adds an owner and clears the domain", () => {
    render(<CatalogTab />);
    fireEvent.change(screen.getByLabelText("Owner"), { target: { value: "lee@example.com" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Add" })[0]!);
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(fx.writes).toEqual([
      {
        reference: REF,
        aspect: "owners",
        value: [
          { owner: "sarah@example.com", type: "technical_owner" },
          { owner: "lee@example.com", type: "technical_owner" },
        ],
        expect: [{ owner: "sarah@example.com", type: "technical_owner" }],
      },
      { reference: REF, aspect: "domain", value: null, expect: "mcp:domain:d1" },
    ]);
  });

  it("adds a context document with an id and a title", () => {
    render(<CatalogTab />);
    fireEvent.click(screen.getByRole("button", { name: "Add document" }));
    fireEvent.change(screen.getByLabelText("Document title"), { target: { value: "Grain" } });
    fireEvent.change(screen.getByLabelText("Document content"), { target: { value: "One row per store." } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const write = fx.writes[0] as { aspect: string; value: { id: string; title: string; content: string }[] };
    expect(write.aspect).toBe("context_documents");
    expect(write.value).toHaveLength(1);
    expect(write.value[0]).toMatchObject({ title: "Grain", content: "One row per store." });
    expect(write.value[0]!.id).not.toBe("");
  });

  it("shows nothing editable to a reader who may not write", () => {
    fx.dataset = dataset({ writable: false });
    render(<CatalogTab />);
    expect(screen.queryByRole("button", { name: "Edit description" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Add tag")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Edit column/ })).not.toBeInTheDocument();
  });

  it("rolls a change back by its id", () => {
    fx.history = [
      {
        id: "c9",
        reference: REF,
        aspect: "description",
        version: 2,
        previous: "Old.",
        value: "Daily sales by store.",
        written_by: "sarah@example.com",
        written_at: "2026-10-02T00:00:00Z",
      },
    ];
    render(<CatalogTab />);
    expect(screen.getByText("was: Old.")).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: /Roll back the Description change/ })[0]!);
    expect(fx.rollbacks).toEqual([{ reference: REF, aspect: "description", change_id: "c9" }]);
  });

  it("opens an upstream dataset in place and goes back to the list", () => {
    const up = "mcp:dataset:(trino,acme,raw.events.clickstream)";
    render(<CatalogTab />);
    fireEvent.click(screen.getByRole("link", { name: "clickstream" }));
    expect(new URLSearchParams(window.location.search).get("ref")).toBe(up);
    fireEvent.click(screen.getByRole("button", { name: /Back to tables/ }));
    expect(window.location.search).toBe("");
    expect(screen.getByRole("button", { name: /daily_sales/ })).toBeInTheDocument();
  });

  it("says so when the dataset cannot be opened", () => {
    fx.dataset = undefined;
    render(<CatalogTab />);
    expect(screen.getByText("Dataset not found.")).toBeInTheDocument();
  });
});
