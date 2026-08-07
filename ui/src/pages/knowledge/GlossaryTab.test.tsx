import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";

vi.mock("@/api/portal/datahub", () => ({
  // catalog/utils.ts re-exports the search threshold, so the mock has to carry
  // it even though nothing on this surface searches.
  MIN_SEARCH_LEN: 2,
  GLOSSARY_PAGE_LIMIT: 100,
  useGlossaryRoots: vi.fn(),
  useGlossaryChildren: vi.fn(),
  useGlossaryParents: vi.fn(),
  useGlossaryTerm: vi.fn(),
  useGlossaryTermUsage: vi.fn(),
  useGlossaryTermColumnUsage: vi.fn(),
  useEntityDocuments: vi.fn(),
  useCreateGlossaryTerm: vi.fn(),
  useCreateGlossaryNode: vi.fn(),
  useDeleteGlossaryEntity: vi.fn(),
  useUpdateDescription: vi.fn(),
}));
// The connection picker lives on CatalogSection, not here; only the writable
// lookup is still read from this module.
vi.mock("@/components/knowledge/DataHubConnectionSelect", () => ({
  useConnectionWritable: vi.fn(() => true),
}));
// CodeMirror does not render cleanly in jsdom; stand in a plain textarea. The
// stand-in forwards the label rather than hardcoding one, so a test that reads
// the editor by its accessible name is still reading the name the component
// asked for (#1200). MarkdownRenderer is left real: what the render path has to
// prove is that markdown arrives formatted, which a stubbed renderer cannot show.
vi.mock("@/components/MarkdownEditor", () => ({
  MarkdownEditor: ({
    value,
    onChange,
    label,
  }: {
    value: string;
    onChange: (v: string) => void;
    label?: string;
  }) => (
    <textarea
      data-testid="markdown-editor"
      aria-label={label}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}));

let mockIsAdmin = true;
let mockTools: string[] = [];
// The knowledge-page backlinks panel on a detail view reads through react-query;
// these tests render without a provider, so the reverse lookup is stubbed and
// mockBacklinks is what it returns.
let mockBacklinks: { id: string; slug: string; title: string }[] = [];
vi.mock("@/api/portal/hooks", () => ({
  useKnowledgeBacklinks: () => ({ data: { pages: mockBacklinks } }),
}));

vi.mock("@/stores/auth", () => ({
  useAuthStore: (sel: (s: unknown) => unknown) =>
    sel({ user: { tools: mockTools }, isAdmin: () => mockIsAdmin }),
}));

import { MARKDOWN_DESCRIPTION, expectRenderedMarkdown } from "@/test/markdownDescription";
import { ApiError } from "@/api/portal/client";
import { GlossaryTab } from "./GlossaryTab";
import {
  GLOSSARY_PAGE_LIMIT,
  useGlossaryRoots,
  useGlossaryChildren,
  useGlossaryParents,
  useGlossaryTerm,
  useGlossaryTermUsage,
  useGlossaryTermColumnUsage,
  useEntityDocuments,
  useCreateGlossaryTerm,
  useCreateGlossaryNode,
  useDeleteGlossaryEntity,
  useUpdateDescription,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";

const q = (data: unknown) => ({ data, isLoading: false, isError: false }) as never;
const failed = { data: undefined, isLoading: false, isError: true } as never;
// lastCall reads the arguments of a mock's most recent call.
const lastCall = <T,>(calls: T[]): T | undefined => calls[calls.length - 1];
const noopMut = () =>
  ({ mutate: vi.fn(), isPending: false, isError: false, isSuccess: false, error: null }) as never;
const mut = (mutate: () => void) =>
  ({ mutate, isPending: false, isError: false, isSuccess: false, error: null }) as never;

const finance = {
  urn: "urn:li:glossaryNode:finance",
  name: "Finance",
  description: "Revenue, billing, and reporting vocabulary.",
  terms_count: 1,
  nodes_count: 1,
};
const billing = {
  urn: "urn:li:glossaryNode:billing",
  name: "Billing",
  parent_node: finance.urn,
  terms_count: 0,
  nodes_count: 0,
};
const revenue = {
  urn: "urn:li:glossaryTerm:Revenue",
  name: "Revenue",
  description: "Gross revenue, excluding refunds.",
};
const netSales = { urn: "urn:li:glossaryTerm:NetSales", name: "Net Sales" };

const dailySales = {
  urn: "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.public.daily_sales,PROD)",
  name: "daily_sales",
  description: "Daily aggregated sales.",
};
const clickstream = {
  urn: "urn:li:dataset:(urn:li:dataPlatform:trino,raw.events.clickstream,PROD)",
  name: "clickstream",
};

const roots = { nodes: [finance], nodes_total: 1, terms: [netSales], terms_total: 1 };
const financeChildren = {
  nodes: [billing],
  terms: [revenue],
  start: 0,
  count: 2,
  total: 2,
};

// openRevenue walks the tree the way a reader does: into Finance, then onto the
// term inside it.
function openRevenue() {
  fireEvent.click(screen.getByText("Finance"));
  fireEvent.click(screen.getByText("Revenue"));
}

// setLocation puts the browser at a URL, the way arriving from a knowledge
// page's citation does. The tab reads its deep link from window.location.
function setLocation(url: string) {
  window.history.replaceState(null, "", url);
}

beforeEach(() => {
  mockIsAdmin = true;
  mockTools = [];
  mockBacklinks = [];
  setLocation("/knowledge/catalog");
  vi.mocked(useConnectionWritable).mockReturnValue(true);
  vi.mocked(useGlossaryTerm).mockReturnValue(q(undefined));
  vi.mocked(useGlossaryRoots).mockReturnValue(q(roots));
  vi.mocked(useGlossaryChildren).mockReturnValue(q(financeChildren));
  vi.mocked(useGlossaryParents).mockReturnValue(q([finance]));
  vi.mocked(useGlossaryTermUsage).mockReturnValue(q([dailySales, clickstream]));
  vi.mocked(useGlossaryTermColumnUsage).mockReturnValue(q([dailySales]));
  vi.mocked(useEntityDocuments).mockReturnValue(
    q([{ urn: "urn:li:document:doc-2", title: "Revenue definition", sub_type: "note", snippet: "Gross, not net." }]),
  );
  [
    useCreateGlossaryTerm,
    useCreateGlossaryNode,
    useDeleteGlossaryEntity,
    useUpdateDescription,
  ].forEach((h) => vi.mocked(h).mockImplementation(noopMut));
});

describe("GlossaryTab", () => {
  it("lists the root branch: its nodes and the terms with no parent", () => {
    render(<GlossaryTab conn="primary" />);
    expect(screen.getByText("Finance")).toBeInTheDocument();
    expect(screen.getByText("Revenue, billing, and reporting vocabulary.")).toBeInTheDocument();
    expect(screen.getByText("Net Sales")).toBeInTheDocument();
    // A term with no definition says so rather than rendering an empty line.
    expect(screen.getByText("No description")).toBeInTheDocument();
    // The root read is the roots route; nothing asks for children until a node
    // is opened.
    expect(vi.mocked(useGlossaryChildren).mock.calls.every((c) => c[1] === null)).toBe(true);
  });

  it("walks into a node and lists what is inside it", () => {
    render(<GlossaryTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));

    expect(screen.getByRole("heading", { name: /Finance/ })).toBeInTheDocument();
    expect(screen.getByText("urn:li:glossaryNode:finance")).toBeInTheDocument();
    expect(screen.getByText("Billing")).toBeInTheDocument();
    expect(screen.getByText("Revenue")).toBeInTheDocument();
    expect(vi.mocked(useGlossaryChildren)).toHaveBeenCalledWith("primary", finance.urn);
  });

  it("shows where an entity sits from the parent chain, not the path walked", () => {
    render(<GlossaryTab conn="primary" />);
    openRevenue();

    const crumbs = screen.getByRole("navigation", { name: "Glossary location" });
    expect(crumbs).toHaveTextContent("Glossary");
    expect(crumbs).toHaveTextContent("Finance");
    expect(crumbs).toHaveTextContent("Revenue");
    expect(vi.mocked(useGlossaryParents)).toHaveBeenCalledWith("primary", revenue.urn);
  });

  it("returns to a branch from a term's breadcrumb", () => {
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    const crumbs = screen.getByRole("navigation", { name: "Glossary location" });
    fireEvent.click(within(crumbs).getByRole("button", { name: "Finance" }));
    expect(screen.getByRole("heading", { name: /Finance/ })).toBeInTheDocument();
  });

  it("does not place a term at the root when its parent chain failed to load", () => {
    vi.mocked(useGlossaryParents).mockReturnValue(failed);
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    expect(screen.getByText("location unavailable")).toBeInTheDocument();
  });

  it("opens a term with its definition, attached documents, and the tables using it", () => {
    const onNavigate = vi.fn();
    render(<GlossaryTab conn="primary" onNavigate={onNavigate} />);
    openRevenue();

    expect(screen.getByRole("heading", { name: /Revenue/ })).toBeInTheDocument();
    expect(screen.getByText("Gross revenue, excluding refunds.")).toBeInTheDocument();
    expect(screen.getByText("Revenue definition")).toBeInTheDocument();
    expect(screen.getByText("daily_sales")).toBeInTheDocument();
    expect(screen.getByText("clickstream")).toBeInTheDocument();

    // The carrier deep-links into the catalog entity editor rather than
    // reloading the page.
    fireEvent.click(screen.getByText("daily_sales"));
    expect(onNavigate).toHaveBeenCalledWith(
      `/knowledge/catalog?urn=${encodeURIComponent(dailySales.urn)}#tables`,
    );
  });

  it("marks only the tables whose column carries the term", () => {
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    // DataHub's glossaryTerms filter matches a table annotated on the table OR
    // on a column, so without the second read every carrier would read as a
    // column carrier.
    expect(screen.getAllByText("on a column")).toHaveLength(1);
    expect(vi.mocked(useGlossaryTermColumnUsage)).toHaveBeenCalledWith("primary", revenue.urn);
  });

  it("reports a term nothing uses instead of an empty section", () => {
    vi.mocked(useGlossaryTermUsage).mockReturnValue(q([]));
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    expect(
      screen.getByText("No table in this connection is annotated with this term."),
    ).toBeInTheDocument();
  });

  it("reports a term with nothing attached instead of an empty section", () => {
    vi.mocked(useEntityDocuments).mockReturnValue(q([]));
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    expect(screen.getByText(/Nothing is attached/)).toBeInTheDocument();
  });

  it("creates a term inside the branch being browsed", () => {
    const mutate = vi.fn();
    vi.mocked(useCreateGlossaryTerm).mockReturnValue(mut(mutate));
    render(<GlossaryTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    fireEvent.click(screen.getByRole("button", { name: /New term/ }));

    // Create is refused until the term has a name.
    expect(screen.getByRole("button", { name: "Create term" })).toBeDisabled();
    fireEvent.change(screen.getByPlaceholderText("e.g. Net Revenue"), {
      target: { value: "  Gross Margin  " },
    });
    fireEvent.change(screen.getByPlaceholderText(/What this term means/), {
      target: { value: "Revenue less cost of goods." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create term" }));

    expect(mutate).toHaveBeenCalledWith(
      { name: "Gross Margin", definition: "Revenue less cost of goods.", parent_node: finance.urn },
      expect.anything(),
    );
  });

  it("creates a node at the root when no branch is open", () => {
    const mutate = vi.fn();
    vi.mocked(useCreateGlossaryNode).mockReturnValue(mut(mutate));
    render(<GlossaryTab conn="primary" />);
    fireEvent.click(screen.getByRole("button", { name: /New node/ }));

    expect(screen.getByText("Created at the root of the glossary.")).toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText("e.g. Finance"), { target: { value: "Supply" } });
    fireEvent.click(screen.getByRole("button", { name: "Create node" }));

    expect(mutate).toHaveBeenCalledWith(
      { name: "Supply", definition: undefined, parent_node: undefined },
      expect.anything(),
    );
  });

  it("edits a definition through the entity-description write", () => {
    const mutate = vi.fn();
    vi.mocked(useUpdateDescription).mockReturnValue(mut(mutate));
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    fireEvent.click(screen.getByRole("button", { name: "Edit description" }));

    const box = screen.getByLabelText("Term definition");
    expect(box).toHaveValue("Gross revenue, excluding refunds.");
    fireEvent.change(box, { target: { value: "Gross revenue before refunds." } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    // A glossary definition is edited through the one entity-description route:
    // the platform routes the write by entity type.
    expect(mutate).toHaveBeenCalledWith(
      { urn: revenue.urn, description: "Gross revenue before refunds." },
      expect.anything(),
    );
  });

  it("renders a term definition as markdown, not as literal source", () => {
    vi.mocked(useGlossaryChildren).mockReturnValue(
      q({ ...financeChildren, terms: [{ ...revenue, description: MARKDOWN_DESCRIPTION }] }),
    );
    render(<GlossaryTab conn="primary" />);
    openRevenue();

    expectRenderedMarkdown();
  });

  it("renders a node definition as markdown, not as literal source", () => {
    vi.mocked(useGlossaryRoots).mockReturnValue(
      q({ ...roots, nodes: [{ ...finance, description: MARKDOWN_DESCRIPTION }] }),
    );
    render(<GlossaryTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));

    expectRenderedMarkdown();
  });

  it("round-trips markdown through the definition editor with its source intact", () => {
    const mutate = vi.fn();
    vi.mocked(useUpdateDescription).mockReturnValue(mut(mutate));
    vi.mocked(useGlossaryChildren).mockReturnValue(
      q({ ...financeChildren, terms: [{ ...revenue, description: MARKDOWN_DESCRIPTION }] }),
    );
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    fireEvent.click(screen.getByRole("button", { name: "Edit description" }));

    // The edit state is the split source/preview markdown editor, opened on the
    // markdown source rather than on the rendered text, so the steward edits
    // what is stored.
    const box = screen.getByTestId("markdown-editor");
    expect(box).toBe(screen.getByLabelText("Term definition"));
    expect(box).toHaveValue(MARKDOWN_DESCRIPTION);
    fireEvent.change(box, { target: { value: `${MARKDOWN_DESCRIPTION}\n\n> Excludes refunds.` } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(mutate).toHaveBeenCalledWith(
      { urn: revenue.urn, description: `${MARKDOWN_DESCRIPTION}\n\n> Excludes refunds.` },
      expect.anything(),
    );
  });

  it("names the definition editor for assistive technology", () => {
    render(<GlossaryTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    fireEvent.click(screen.getByRole("button", { name: "Edit description" }));

    // The markdown editor carries the name the textarea carried: the label is
    // passed through to the editing surface rather than dropped at the swap.
    expect(screen.getByTestId("markdown-editor")).toBe(screen.getByLabelText("Node definition"));
  });

  it("shows what uses a term before confirming its delete", () => {
    const mutate = vi.fn();
    vi.mocked(useDeleteGlossaryEntity).mockReturnValue(mut(mutate));
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    fireEvent.click(screen.getByRole("button", { name: "Delete term" }));

    expect(
      screen.getByText(/2 tables in this connection are annotated with this term/),
    ).toBeInTheDocument();
    // The delete touches only the term entity, so the confirmation says the
    // annotations are left behind rather than cleared.
    expect(screen.getByText(/does not remove the annotation from those tables/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    expect(mutate).toHaveBeenCalledWith(revenue.urn, expect.anything());
  });

  it("does not claim a term is unused when the usage read failed", () => {
    vi.mocked(useGlossaryTermUsage).mockReturnValue(failed);
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    fireEvent.click(screen.getByRole("button", { name: "Delete term" }));
    expect(screen.getByText(/effect of deleting it is unknown/)).toBeInTheDocument();
    expect(
      screen.queryByText("No table in this connection is annotated with this term."),
    ).not.toBeInTheDocument();
  });

  it("reports a capped usage count as a floor in the delete confirmation", () => {
    const many = Array.from({ length: GLOSSARY_PAGE_LIMIT }, (_, i) => ({
      urn: `urn:li:dataset:(urn:li:dataPlatform:trino,d${i},PROD)`,
      name: `d${i}`,
    }));
    vi.mocked(useGlossaryTermUsage).mockReturnValue(q(many));
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    fireEvent.click(screen.getByRole("button", { name: "Delete term" }));
    expect(screen.getByText(new RegExp(`At least ${GLOSSARY_PAGE_LIMIT} tables`))).toBeInTheDocument();
  });

  it("refuses to delete a node that still holds entries, and says why", () => {
    render(<GlossaryTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    expect(screen.queryByRole("button", { name: "Delete node" })).not.toBeInTheDocument();
    expect(screen.getByText(/This node holds 2 entries/)).toBeInTheDocument();
  });

  it("deletes an empty node", () => {
    const mutate = vi.fn();
    vi.mocked(useDeleteGlossaryEntity).mockReturnValue(mut(mutate));
    vi.mocked(useGlossaryChildren).mockReturnValue(q({ nodes: [], terms: [], start: 0, count: 0, total: 0 }));
    render(<GlossaryTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));

    expect(screen.getByText("This node is empty.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Delete node" }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    expect(mutate).toHaveBeenCalledWith(finance.urn, expect.anything());
  });

  it("does not offer a node delete when what is inside it could not be read", () => {
    // The delete is offered only on a read that answered empty. A failed read
    // must not fall through to "this node is empty", which it never established.
    vi.mocked(useGlossaryChildren).mockReturnValue(failed);
    render(<GlossaryTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    expect(screen.queryByRole("button", { name: "Delete node" })).not.toBeInTheDocument();
    expect(screen.getByText(/Could not read what is in this node/)).toBeInTheDocument();
    expect(screen.queryByText("This node is empty.")).not.toBeInTheDocument();
  });

  it("does not offer a node delete while the read is still in flight", () => {
    vi.mocked(useGlossaryChildren).mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
    } as never);
    render(<GlossaryTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    expect(screen.queryByRole("button", { name: "Delete node" })).not.toBeInTheDocument();
    expect(screen.getByText(/Checking what is in this node/)).toBeInTheDocument();
  });

  it("says how much of a wide branch it is showing", () => {
    vi.mocked(useGlossaryRoots).mockReturnValue(q({ ...roots, nodes_total: 40, terms_total: 12 }));
    render(<GlossaryTab conn="primary" />);
    expect(screen.getByText(/Showing 2 of 52/)).toBeInTheDocument();
  });

  it("surfaces a failed delete instead of silently returning", () => {
    vi.mocked(useDeleteGlossaryEntity).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      isError: true,
      error: null,
    } as never);
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });

  it("reports a failed glossary read rather than an empty glossary", () => {
    vi.mocked(useGlossaryRoots).mockReturnValue(failed);
    render(<GlossaryTab conn="primary" />);
    expect(screen.getByText("Failed to load the glossary.")).toBeInTheDocument();
  });

  it("hides every write affordance on a read-only connection", () => {
    vi.mocked(useConnectionWritable).mockReturnValue(false);
    render(<GlossaryTab conn="primary" />);
    expect(screen.queryByRole("button", { name: /New term/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /New node/ })).not.toBeInTheDocument();
    openRevenue();
    expect(screen.queryByRole("button", { name: "Delete term" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit description" })).not.toBeInTheDocument();
    // The read surfaces stay.
    expect(screen.getByText("Tables annotated with this term")).toBeInTheDocument();
    expect(screen.getByText("Gross revenue, excluding refunds.")).toBeInTheDocument();
  });

  it("hides each write affordance the persona does not grant", () => {
    mockIsAdmin = false;
    mockTools = ["datahub_update"];
    render(<GlossaryTab conn="primary" />);
    // Update alone grants the definition edit, not create or delete.
    expect(screen.queryByRole("button", { name: /New term/ })).not.toBeInTheDocument();
    openRevenue();
    expect(screen.getByRole("button", { name: "Edit description" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete term" })).not.toBeInTheDocument();
  });

  // #1159: a knowledge page citing a term links to
  // /knowledge/catalog?urn=…#glossary, and this tab is what opens on the other
  // end. A term is read by URN, so it opens wherever it sits in the tree —
  // unlike a tag or a domain, which are matched against a listed vocabulary.
  describe("deep link from a knowledge page", () => {
    it("opens the linked term without walking the tree to it", () => {
      vi.mocked(useGlossaryTerm).mockReturnValue(q(revenue));
      setLocation("/knowledge/catalog?urn=urn%3Ali%3AglossaryTerm%3ARevenue#glossary");
      render(<GlossaryTab conn="primary" />);
      expect(screen.getByRole("heading", { name: /Revenue/ })).toBeInTheDocument();
      expect(screen.getByText("Gross revenue, excluding refunds.")).toBeInTheDocument();
      expect(lastCall(vi.mocked(useGlossaryTerm).mock.calls)).toEqual([
        "primary",
        "urn:li:glossaryTerm:Revenue",
      ]);
    });

    it("reports a term this connection does not hold", () => {
      vi.mocked(useGlossaryTerm).mockReturnValue({
        data: undefined,
        isLoading: false,
        isError: true,
        error: new ApiError(404, "glossary term read failed"),
      } as never);
      setLocation("/knowledge/catalog?urn=urn%3Ali%3AglossaryTerm%3AElsewhere#glossary");
      render(<GlossaryTab conn="primary" />);
      expect(screen.getByText(/has no glossary term with the URN/)).toBeInTheDocument();
    });

    it("does not call a failed read a missing term", () => {
      // A 502 says the catalog did not answer; reporting the term as retired
      // from that would be a claim the read never established.
      vi.mocked(useGlossaryTerm).mockReturnValue({
        data: undefined,
        isLoading: false,
        isError: true,
        error: new ApiError(502, "datahub down"),
      } as never);
      setLocation("/knowledge/catalog?urn=urn%3Ali%3AglossaryTerm%3ARevenue#glossary");
      render(<GlossaryTab conn="primary" />);
      expect(screen.getByText("Failed to load the linked glossary term.")).toBeInTheDocument();
      expect(screen.queryByText(/has no glossary term with the URN/)).not.toBeInTheDocument();
    });

    it("drops the deep link on the way back, so a refresh does not reopen it", () => {
      vi.mocked(useGlossaryTerm).mockReturnValue(q(revenue));
      setLocation("/knowledge/catalog?urn=urn%3Ali%3AglossaryTerm%3ARevenue#glossary");
      render(<GlossaryTab conn="primary" />);
      fireEvent.click(screen.getByRole("button", { name: /Back to the glossary/ }));
      expect(window.location.search).toBe("");
      expect(screen.getByRole("heading", { name: "Glossary" })).toBeInTheDocument();
    });

    it("ignores a URN that belongs to another tab", () => {
      setLocation("/knowledge/catalog?urn=urn%3Ali%3Atag%3Apii#glossary");
      render(<GlossaryTab conn="primary" />);
      expect(screen.getByRole("heading", { name: "Glossary" })).toBeInTheDocument();
    });
  });

  it("lists the knowledge pages that reference the term", () => {
    mockBacklinks = [{ id: "kp1", slug: "revenue-guidance", title: "Revenue Guidance" }];
    render(<GlossaryTab conn="primary" />);
    openRevenue();
    expect(screen.getByText("Revenue Guidance")).toBeInTheDocument();
    expect(screen.getByText(/1 knowledge page references this/)).toBeInTheDocument();
  });
});
