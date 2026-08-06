import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";

vi.mock("@/api/portal/datahub", () => ({
  MIN_SEARCH_LEN: 2,
  DOMAIN_LIST_LIMIT: 100,
  DOMAIN_MEMBER_LIMIT: 100,
  useDomainList: vi.fn(),
  useDomainMembers: vi.fn(),
  useCreateDomain: vi.fn(),
  useDeleteDomain: vi.fn(),
  useUpdateDomain: vi.fn(),
  useCatalogSearch: vi.fn(),
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
import { DomainsTab } from "./DomainsTab";
import {
  DOMAIN_LIST_LIMIT,
  DOMAIN_MEMBER_LIMIT,
  useDomainList,
  useDomainMembers,
  useCreateDomain,
  useDeleteDomain,
  useUpdateDomain,
  useCatalogSearch,
  useUpdateDescription,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";

const q = (data: unknown) => ({ data, isLoading: false, isError: false }) as never;
const noopMut = () =>
  ({ mutate: vi.fn(), isPending: false, isError: false, isSuccess: false, error: null }) as never;

const finance = {
  urn: "urn:li:domain:finance",
  name: "Finance",
  description: "Revenue, billing, and reporting.",
};
const marketing = { urn: "urn:li:domain:marketing", name: "Marketing" };
const member = {
  urn: "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.public.daily_sales,PROD)",
  name: "daily_sales",
  description: "Daily aggregated sales.",
};
const outsider = {
  urn: "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.public.customers,PROD)",
  name: "customers",
};

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
  vi.mocked(useDomainList).mockReturnValue(q([finance, marketing]));
  vi.mocked(useDomainMembers).mockReturnValue(q([member]));
  vi.mocked(useCatalogSearch).mockReturnValue(q([member, outsider]));
  [useCreateDomain, useDeleteDomain, useUpdateDomain, useUpdateDescription].forEach((h) =>
    vi.mocked(h).mockImplementation(noopMut),
  );
});

describe("DomainsTab", () => {
  it("lists domains with their descriptions", () => {
    render(<DomainsTab conn="primary" />);
    expect(screen.getByText("Finance")).toBeInTheDocument();
    expect(screen.getByText("Revenue, billing, and reporting.")).toBeInTheDocument();
    // A domain with no description says so rather than rendering an empty line.
    expect(screen.getByText("No description")).toBeInTheDocument();
  });

  it("filters the loaded list client-side rather than refetching", () => {
    render(<DomainsTab conn="primary" />);
    fireEvent.change(screen.getByPlaceholderText("Filter domains by name…"), {
      target: { value: "mark" },
    });

    expect(screen.getByText("Marketing")).toBeInTheDocument();
    expect(screen.queryByText("Finance")).not.toBeInTheDocument();
    // DataHub has no name-scoped domain search, so the filter never reaches the
    // server: the list hook is still called with the connection alone.
    expect(vi.mocked(useDomainList).mock.calls.every((c) => c[0] === "primary")).toBe(true);
    expect(vi.mocked(useDomainList).mock.calls.every((c) => c.length === 1)).toBe(true);
  });

  it("reports a filter miss rather than an empty connection", () => {
    render(<DomainsTab conn="primary" />);
    fireEvent.change(screen.getByPlaceholderText("Filter domains by name…"), {
      target: { value: "nothing-matches" },
    });
    expect(screen.getByText("No domains match that name.")).toBeInTheDocument();
  });

  it("opens a domain and shows the tables in it", () => {
    const onNavigate = vi.fn();
    render(<DomainsTab conn="primary" onNavigate={onNavigate} />);
    fireEvent.click(screen.getByText("Finance"));

    expect(screen.getByRole("heading", { name: /Finance/ })).toBeInTheDocument();
    expect(screen.getByText("urn:li:domain:finance")).toBeInTheDocument();
    expect(screen.getByText("Tables in this domain")).toBeInTheDocument();

    // The member deep-links into the catalog entity editor rather than
    // reloading the page.
    fireEvent.click(screen.getByText("daily_sales"));
    expect(onNavigate).toHaveBeenCalledWith(
      `/knowledge/catalog?urn=${encodeURIComponent(member.urn)}#tables`,
    );
  });

  it("reports an empty membership instead of an empty section", () => {
    vi.mocked(useDomainMembers).mockReturnValue(q([]));
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    expect(screen.getByText("No table in this connection is in this domain.")).toBeInTheDocument();
  });

  it("creates a domain with its name and description", () => {
    const mutate = vi.fn();
    vi.mocked(useCreateDomain).mockReturnValue({
      mutate,
      isPending: false,
      isError: false,
      error: null,
    } as never);
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByRole("button", { name: "New domain" }));

    // Create is refused until the domain has a name.
    expect(screen.getByRole("button", { name: "Create domain" })).toBeDisabled();
    fireEvent.change(screen.getByPlaceholderText("e.g. Finance"), {
      target: { value: "  Supply Chain  " },
    });
    fireEvent.change(screen.getByPlaceholderText(/What this domain covers/), {
      target: { value: "Procurement and logistics." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create domain" }));

    expect(mutate).toHaveBeenCalledWith(
      { name: "Supply Chain", description: "Procurement and logistics." },
      expect.anything(),
    );
  });

  it("edits a domain description through the entity-description write", () => {
    const mutate = vi.fn();
    vi.mocked(useUpdateDescription).mockReturnValue({
      mutate,
      isPending: false,
      isError: false,
      isSuccess: false,
      error: null,
    } as never);
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    fireEvent.click(screen.getByRole("button", { name: "Edit description" }));

    const box = screen.getByLabelText("Domain description");
    expect(box).toHaveValue("Revenue, billing, and reporting.");
    fireEvent.change(box, { target: { value: "Everything money touches." } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(mutate).toHaveBeenCalledWith(
      { urn: "urn:li:domain:finance", description: "Everything money touches." },
      expect.anything(),
    );
  });

  it("renders a domain description as markdown, not as literal source", () => {
    vi.mocked(useDomainList).mockReturnValue(
      q([{ ...finance, description: MARKDOWN_DESCRIPTION }, marketing]),
    );
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));

    expectRenderedMarkdown();
  });

  it("round-trips markdown through the description editor with its source intact", () => {
    const mutate = vi.fn();
    vi.mocked(useUpdateDescription).mockReturnValue({
      mutate,
      isPending: false,
      isError: false,
      isSuccess: false,
      error: null,
    } as never);
    vi.mocked(useDomainList).mockReturnValue(
      q([{ ...finance, description: MARKDOWN_DESCRIPTION }, marketing]),
    );
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    fireEvent.click(screen.getByRole("button", { name: "Edit description" }));

    // The edit state is the split source/preview markdown editor, opened on the
    // markdown source rather than on the rendered text, and it carries the name
    // the textarea carried before the swap.
    const box = screen.getByTestId("markdown-editor");
    expect(box).toBe(screen.getByLabelText("Domain description"));
    expect(box).toHaveValue(MARKDOWN_DESCRIPTION);
    fireEvent.change(box, { target: { value: `${MARKDOWN_DESCRIPTION}\n\n> Excludes refunds.` } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(mutate).toHaveBeenCalledWith(
      { urn: "urn:li:domain:finance", description: `${MARKDOWN_DESCRIPTION}\n\n> Excludes refunds.` },
      expect.anything(),
    );
  });

  it("shows what is in a domain before confirming its delete", () => {
    const mutate = vi.fn();
    vi.mocked(useDeleteDomain).mockReturnValue({
      mutate,
      isPending: false,
      isError: false,
      error: null,
    } as never);
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    fireEvent.click(screen.getByRole("button", { name: "Delete domain" }));

    expect(screen.getByText(/1 table in this connection is in this domain/)).toBeInTheDocument();
    // The delete touches only the domain entity, so the confirmation says the
    // tables are left behind rather than removed.
    expect(screen.getByText(/leaves those tables without a domain/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    expect(mutate).toHaveBeenCalledWith("urn:li:domain:finance", expect.anything());
  });

  it("does not claim a domain is empty when the membership read failed", () => {
    vi.mocked(useDomainMembers).mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
    } as never);
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    fireEvent.click(screen.getByRole("button", { name: "Delete domain" }));
    expect(screen.getByText(/effect of deleting it is unknown/)).toBeInTheDocument();
    expect(screen.queryByText(/No table in this connection is in this domain/)).not.toBeInTheDocument();
  });

  it("removes a table from the domain by clearing the table's domain", () => {
    const mutate = vi.fn();
    vi.mocked(useUpdateDomain).mockReturnValue({
      mutate,
      isPending: false,
      isError: false,
      error: null,
    } as never);
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    fireEvent.click(screen.getByRole("button", { name: /Remove daily_sales from this domain/ }));

    // The write targets the table, not the domain: DataHub stores the domain on
    // the table.
    expect(mutate).toHaveBeenCalledWith({ urn: member.urn, clear_domain: true });
  });

  it("adds a searched table to the domain and hides those already in it", () => {
    vi.useFakeTimers();
    try {
      const mutate = vi.fn();
      vi.mocked(useUpdateDomain).mockReturnValue({
        mutate,
        isPending: false,
        isError: false,
        error: null,
      } as never);
      render(<DomainsTab conn="primary" />);
      fireEvent.click(screen.getByText("Finance"));
      fireEvent.change(screen.getByPlaceholderText("Search tables by name…"), {
        target: { value: "analytics" },
      });
      act(() => vi.advanceTimersByTime(300));

      // The search returned both tables but daily_sales is already in the
      // domain, so exactly one candidate is offered.
      expect(screen.getAllByRole("button", { name: "Add" })).toHaveLength(1);
      expect(screen.getByText("customers")).toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "Add" }));

      expect(mutate).toHaveBeenCalledWith(
        { urn: outsider.urn, domain: "urn:li:domain:finance" },
        expect.anything(),
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it("surfaces a failed membership write instead of leaving the row unchanged", () => {
    // A refused remove and a remove that has not refreshed yet look identical
    // without this: the row is still there either way.
    vi.mocked(useUpdateDomain).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      isError: true,
      error: null,
    } as never);
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });

  it("surfaces a failed delete instead of silently returning", () => {
    vi.mocked(useDeleteDomain).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      isError: true,
      error: null,
    } as never);
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });

  it("hides every write affordance on a read-only connection", () => {
    vi.mocked(useConnectionWritable).mockReturnValue(false);
    render(<DomainsTab conn="primary" />);
    expect(screen.queryByRole("button", { name: "New domain" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("Finance"));
    expect(screen.queryByRole("button", { name: "Delete domain" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit description" })).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText("Search tables by name…")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Remove daily_sales/ })).not.toBeInTheDocument();
    // The read surfaces stay.
    expect(screen.getByText("Tables in this domain")).toBeInTheDocument();
    expect(screen.getByText("Revenue, billing, and reporting.")).toBeInTheDocument();
  });

  it("hides each write affordance the persona does not grant", () => {
    mockIsAdmin = false;
    mockTools = ["datahub_update"];
    render(<DomainsTab conn="primary" />);
    // Update alone grants the description edit and membership editing, not
    // create or delete.
    expect(screen.queryByRole("button", { name: "New domain" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("Finance"));
    expect(screen.getByRole("button", { name: "Edit description" })).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Search tables by name…")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete domain" })).not.toBeInTheDocument();
  });

  it("says the domain list is capped by DataHub rather than presenting it as complete", () => {
    const many = Array.from({ length: DOMAIN_LIST_LIMIT }, (_, i) => ({
      urn: `urn:li:domain:d${i}`,
      name: `d${i}`,
    }));
    vi.mocked(useDomainList).mockReturnValue(q(many));
    render(<DomainsTab conn="primary" />);
    expect(
      screen.getByText(new RegExp(`Showing the first ${DOMAIN_LIST_LIMIT} domains`)),
    ).toBeInTheDocument();
  });

  it("reports a capped membership count as a floor in the delete confirmation", () => {
    const many = Array.from({ length: DOMAIN_MEMBER_LIMIT }, (_, i) => ({
      urn: `urn:li:dataset:(urn:li:dataPlatform:trino,d${i},PROD)`,
      name: `d${i}`,
    }));
    vi.mocked(useDomainMembers).mockReturnValue(q(many));
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    fireEvent.click(screen.getByRole("button", { name: "Delete domain" }));
    expect(
      screen.getByText(new RegExp(`At least ${DOMAIN_MEMBER_LIMIT} tables`)),
    ).toBeInTheDocument();
  });

  it("reports a failed domain list rather than an empty one", () => {
    vi.mocked(useDomainList).mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
    } as never);
    render(<DomainsTab conn="primary" />);
    expect(screen.getByText("Failed to load domains.")).toBeInTheDocument();
  });

  // #1159: a knowledge page citing a domain links to
  // /knowledge/catalog?urn=…#domains, and this tab is what opens on the other end.
  describe("deep link from a knowledge page", () => {
    it("opens the linked domain instead of the list", () => {
      setLocation("/knowledge/catalog?urn=urn%3Ali%3Adomain%3Afinance#domains");
      render(<DomainsTab conn="primary" />);
      expect(screen.getByRole("heading", { name: /Finance/ })).toBeInTheDocument();
      expect(screen.getByText("Revenue, billing, and reporting.")).toBeInTheDocument();
    });

    it("says so when this connection does not list the domain", () => {
      // The domain list is capped upstream at 100 and has no by-URN read, so a
      // URN it does not hold cannot be opened; saying that beats a detail view
      // with a blank description that reads as "this domain is undocumented".
      setLocation("/knowledge/catalog?urn=urn%3Ali%3Adomain%3Aelsewhere#domains");
      render(<DomainsTab conn="primary" />);
      expect(screen.getByText(/lists no domain with the URN/)).toBeInTheDocument();
    });

    it("drops the deep link on the way back, so a refresh does not reopen it", () => {
      setLocation("/knowledge/catalog?urn=urn%3Ali%3Adomain%3Afinance#domains");
      render(<DomainsTab conn="primary" />);
      fireEvent.click(screen.getByRole("button", { name: /Back to domains/ }));
      expect(window.location.search).toBe("");
      expect(screen.getByPlaceholderText("Filter domains by name…")).toBeInTheDocument();
    });

    it("ignores a URN that belongs to another tab", () => {
      setLocation("/knowledge/catalog?urn=urn%3Ali%3Atag%3Apii#domains");
      render(<DomainsTab conn="primary" />);
      expect(screen.getByPlaceholderText("Filter domains by name…")).toBeInTheDocument();
    });
  });

  it("lists the knowledge pages that reference the domain", () => {
    mockBacklinks = [{ id: "kp1", slug: "finance-domain", title: "Finance Domain Guide" }];
    render(<DomainsTab conn="primary" />);
    fireEvent.click(screen.getByText("Finance"));
    expect(screen.getByText("Finance Domain Guide")).toBeInTheDocument();
    expect(screen.getByText(/1 knowledge page references this/)).toBeInTheDocument();
  });
});
