import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

// Mock the DataHub API module so the tab renders against controlled data with no
// network. Each hook is a vi.fn configured per test.
vi.mock("@/api/portal/datahub", () => ({
  MIN_SEARCH_LEN: 2,
  useCatalogBrowse: vi.fn(),
  useCatalogSearch: vi.fn(),
  useCatalogEntity: vi.fn(),
  useUpdateDescription: vi.fn(),
  useUpdateTags: vi.fn(),
  useUpdateOwners: vi.fn(),
  useUpdateGlossaryTerms: vi.fn(),
  useUpdateDomain: vi.fn(),
  useTagLookup: vi.fn(),
  useGlossaryLookup: vi.fn(),
  useDomainLookup: vi.fn(),
}));
// The connection picker lives on CatalogSection, not here; only the writable
// lookup is still read from this module.
vi.mock("@/components/knowledge/DataHubConnectionSelect", () => ({
  useConnectionWritable: vi.fn(() => true),
}));
// CodeMirror does not render cleanly in jsdom; stand in a plain textarea. The
// markdown renderer is left real so these tests see the formatting a reader
// gets.
vi.mock("@/components/MarkdownEditor", () => ({
  MarkdownEditor: ({ value, onChange }: { value: string; onChange: (v: string) => void }) => (
    <textarea aria-label="description" value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

let mockIsAdmin = true;
let mockTools: string[] = [];
vi.mock("@/stores/auth", () => ({
  useAuthStore: (sel: (s: unknown) => unknown) =>
    sel({ user: { tools: mockTools }, isAdmin: () => mockIsAdmin }),
}));

import { CatalogTab } from "./CatalogTab";
import {
  useCatalogBrowse,
  useCatalogSearch,
  useCatalogEntity,
  useUpdateDescription,
  useUpdateTags,
  useUpdateOwners,
  useUpdateGlossaryTerms,
  useUpdateDomain,
  useTagLookup,
  useGlossaryLookup,
  useDomainLookup,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { ApiError } from "@/api/portal/client";

const q = (data: unknown) => ({ data, isLoading: false, isError: false }) as never;
const lookupResult = (data: unknown) => ({ data, isFetching: false, isError: false }) as never;
const noopMut = () => ({ mutate: vi.fn(), isPending: false, isError: false, error: null }) as never;

// Descriptions are authored as markdown: DataHub stores the source and the
// platform's own write paths (apply_knowledge, datahub_update) emit it.
const markdownDescription = "**Daily sales.** One row per `order_id`.";

const daily = {
  urn: "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.public.daily_sales,PROD)",
  name: "analytics.public.daily_sales",
  description: markdownDescription,
  tags: ["urn:li:tag:finance"],
};

beforeEach(() => {
  mockIsAdmin = true;
  mockTools = [];
  vi.mocked(useConnectionWritable).mockReturnValue(true);
  vi.mocked(useCatalogBrowse).mockReturnValue(q([daily]));
  vi.mocked(useCatalogSearch).mockReturnValue(q([]));
  vi.mocked(useCatalogEntity).mockReturnValue(
    q({
      urn: daily.urn,
      context: {
        urn: daily.urn,
        description: markdownDescription,
        tags: ["finance"],
        tag_refs: [{ urn: "urn:li:tag:finance", name: "finance" }],
        owners: [{ urn: "urn:li:corpuser:sarah", type: "TECHNICAL_OWNER", name: "Sarah" }],
        glossary_terms: [{ urn: "urn:li:glossaryTerm:Revenue", name: "Revenue" }],
        domain: { urn: "urn:li:domain:finance", name: "Finance" },
      },
      columns: {
        revenue: { name: "revenue", description: "Revenue in **USD**", is_sensitive: true },
      },
    }),
  );
  [useUpdateDescription, useUpdateTags, useUpdateOwners, useUpdateGlossaryTerms, useUpdateDomain].forEach((h) =>
    vi.mocked(h).mockImplementation(noopMut),
  );
  vi.mocked(useTagLookup).mockReturnValue(lookupResult([]));
  vi.mocked(useGlossaryLookup).mockReturnValue(lookupResult([]));
  vi.mocked(useDomainLookup).mockReturnValue(lookupResult([]));
});

describe("CatalogTab", () => {
  it("browses tables and opens an entity showing metadata and columns", () => {
    render(<CatalogTab conn="primary" />);
    expect(screen.getByText("analytics.public.daily_sales")).toBeInTheDocument();

    fireEvent.click(screen.getByText("analytics.public.daily_sales"));
    expect(screen.getByRole("heading", { name: /daily_sales/ })).toBeInTheDocument();
    expect(screen.getByText("Revenue")).toBeInTheDocument();
    expect(screen.getByText("Sarah")).toBeInTheDocument();
    expect(screen.getByText("Finance")).toBeInTheDocument();
    // Column with a sensitivity badge.
    expect(screen.getByText("revenue")).toBeInTheDocument();
    expect(screen.getByText("Sensitive")).toBeInTheDocument();
  });

  it("renders the entity description as markdown, not as source (#1101)", () => {
    render(<CatalogTab conn="primary" />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));

    const bold = document.querySelector("strong");
    expect(bold?.textContent).toBe("Daily sales.");
    expect(document.querySelector("code")?.textContent).toBe("order_id");
    expect(document.body.textContent).not.toContain("**Daily sales.**");
  });

  it("renders column descriptions as markdown (#1101)", () => {
    render(<CatalogTab conn="primary" />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));

    const cell = screen.getByText("revenue").closest("tr")!;
    expect(cell.querySelector("strong")?.textContent).toBe("USD");
    expect(cell.textContent).not.toContain("**USD**");
  });

  it("strips markdown from the search-result snippet (#1101)", () => {
    render(<CatalogTab conn="primary" />);

    expect(screen.getByText("Daily sales. One row per order_id.")).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("**Daily sales.**");
  });

  it("shows edit affordances for a writer and drives a description edit", () => {
    const mutate = vi.fn();
    vi.mocked(useUpdateDescription).mockReturnValue({ mutate, isPending: false, isError: false, error: null } as never);
    render(<CatalogTab conn="primary" />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));

    // Edit the description.
    const editButtons = screen.getAllByRole("button", { name: "Edit" });
    fireEvent.click(editButtons[0]!);
    // The description is edited in the shared markdown editor, stubbed here as a
    // labelled textarea; the tag/owner/domain add fields are <input>s.
    const editor = screen.getByLabelText("description");
    fireEvent.change(editor, { target: { value: "Updated." } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(mutate).toHaveBeenCalledWith(
      { urn: daily.urn, description: "Updated." },
      expect.anything(),
    );
  });

  it("resolves a tag name to a URN through the picker (#785)", () => {
    const mutate = vi.fn();
    vi.mocked(useUpdateTags).mockReturnValue({ mutate, isPending: false, isError: false, error: null } as never);
    vi.mocked(useTagLookup).mockReturnValue(
      lookupResult([{ urn: "urn:li:tag:PII", name: "PII", description: "personal data" }]),
    );
    render(<CatalogTab conn="primary" />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));

    // The user types a display name, never a raw URN, and picks the result.
    const input = screen.getByPlaceholderText("Search tags by name…");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "PI" } });
    fireEvent.click(screen.getByRole("button", { name: /PII/ }));

    expect(mutate).toHaveBeenCalledWith(
      { urn: daily.urn, add: ["urn:li:tag:PII"] },
      expect.anything(),
    );
  });

  it("removes a tag by its URN, not its display name (#785 review)", () => {
    const mutate = vi.fn();
    vi.mocked(useUpdateTags).mockReturnValue({ mutate, isPending: false, isError: false, error: null } as never);
    render(<CatalogTab conn="primary" />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));
    // The existing "finance" tag chip removes by URN so DataHub can match it.
    fireEvent.click(screen.getByRole("button", { name: "Remove finance" }));
    expect(mutate).toHaveBeenCalledWith({ urn: daily.urn, remove: ["urn:li:tag:finance"] });
  });

  it("offers an exact typed tag URN as a fallback candidate (#785 review)", () => {
    const mutate = vi.fn();
    vi.mocked(useUpdateTags).mockReturnValue({ mutate, isPending: false, isError: false, error: null } as never);
    // Name search returns nothing, but the user pastes an exact URN.
    vi.mocked(useTagLookup).mockReturnValue(lookupResult([]));
    render(<CatalogTab conn="primary" />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));
    const input = screen.getByPlaceholderText("Search tags by name…");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "urn:li:tag:Quarantine" } });
    fireEvent.click(screen.getByRole("button", { name: /urn:li:tag:Quarantine/ }));
    expect(mutate).toHaveBeenCalledWith(
      { urn: daily.urn, add: ["urn:li:tag:Quarantine"] },
      expect.anything(),
    );
  });

  it("surfaces a visible inline error when an update fails", () => {
    vi.mocked(useUpdateTags).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      isError: true,
      error: new ApiError(400, 'invalid tag: "test" must be a urn:li:tag:<id> URN'),
    } as never);
    render(<CatalogTab conn="primary" />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));
    const alerts = screen.getAllByRole("alert");
    expect(alerts.some((a) => /must be a urn:li:tag/.test(a.textContent ?? ""))).toBe(true);
  });

  it("hides all edit affordances when the connection is read-only", () => {
    vi.mocked(useConnectionWritable).mockReturnValue(false);
    mockIsAdmin = true;
    render(<CatalogTab conn="primary" />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add" })).not.toBeInTheDocument();
  });

  it("hides edit affordances when the persona lacks datahub_update and is not admin", () => {
    mockIsAdmin = false;
    mockTools = ["datahub_browse"];
    render(<CatalogTab conn="primary" />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
  });
});
